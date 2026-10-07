// Package transfer provides single-file SFTP transfers using temporary files.
// Local path checks reduce symlink and junction exposure, but cannot eliminate
// a concurrent filesystem replacement between validation and use.
package transfer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"awesomeProject/internal/mapping"
	"github.com/google/uuid"
	"github.com/pkg/sftp"
)

const copyChunkSize = 64 * 1024

const posixRenameExtension = "posix-rename@openssh.com"

type progressKey struct{}

// WithProgress reports bytes accepted by the destination writer. The callback
// is synchronous and does not indicate that the temporary file was committed.
func WithProgress(ctx context.Context, report func(int64)) context.Context {
	return context.WithValue(ctx, progressKey{}, report)
}

// CheckRemotePath verifies that an absolute remote directory exists and that
// every component from the POSIX root through the target is a real directory,
// not a symbolic link. It is suitable for validating a directory before
// listing it.
func CheckRemotePath(client *sftp.Client, remotePath string) error {
	if client == nil {
		return errors.New("SFTP client is nil")
	}
	if remotePath == "" || !strings.HasPrefix(remotePath, "/") || strings.ContainsRune(remotePath, '\x00') || path.Clean(remotePath) != remotePath {
		return fmt.Errorf("remote path must be a normalized absolute POSIX path")
	}
	return validateRemoteDirectoryChain(client, remotePath)
}

// Upload copies one local regular file to an absolute remote POSIX path.
// It writes to an exclusive temporary file beside the destination, then
// renames it into place only after the copy and both closes succeed. On
// overwrite it preserves remote permission bits when the server supports
// chmod; ACLs and ownership are not preserved.
func Upload(ctx context.Context, client *sftp.Client, localSource, remoteDestination string, overwrite bool) (retErr error) {
	if client == nil {
		return errors.New("SFTP client is nil")
	}
	if err := contextErr(ctx); err != nil {
		return err
	}
	if err := validateRemoteAbsolute(remoteDestination); err != nil {
		return fmt.Errorf("remote destination: %w", err)
	}
	if err := validateRemoteDirectoryChain(client, path.Dir(remoteDestination)); err != nil {
		return fmt.Errorf("remote destination parent: %w", err)
	}
	if err := validateLocalPath(localSource); err != nil {
		return fmt.Errorf("local source path: %w", err)
	}

	localSourceInfo, err := os.Lstat(localSource)
	if err != nil {
		return fmt.Errorf("lstat local source: %w", err)
	}
	if !localSourceInfo.Mode().IsRegular() || localSourceInfo.Mode()&os.ModeSymlink != 0 || localSourceInfo.Mode()&os.ModeIrregular != 0 {
		return fmt.Errorf("local source is not a regular file")
	}
	source, err := os.Open(localSource)
	if err != nil {
		return fmt.Errorf("open local source: %w", err)
	}
	defer source.Close()
	initialSource, err := source.Stat()
	if err != nil {
		return fmt.Errorf("stat local source: %w", err)
	}
	if !initialSource.Mode().IsRegular() {
		return fmt.Errorf("local source is not a regular file")
	}
	if !os.SameFile(localSourceInfo, initialSource) {
		return fmt.Errorf("local source changed while opening")
	}

	initialTarget, targetExists, err := remoteTargetSnapshot(client, remoteDestination)
	if err != nil {
		return fmt.Errorf("inspect remote destination: %w", err)
	}
	if targetExists && !overwrite {
		return fmt.Errorf("remote destination already exists")
	}

	tempPath := path.Join(path.Dir(remoteDestination), "."+path.Base(remoteDestination)+".transfer-"+uuid.NewString()+".tmp")
	temp, err := client.OpenFile(tempPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return fmt.Errorf("create remote temporary file: %w", err)
	}
	tempExists := true
	defer func() {
		if tempExists {
			if err := client.Remove(tempPath); err != nil && !isNotExist(err) {
				retErr = errors.Join(retErr, fmt.Errorf("remove remote temporary file %q: %w", tempPath, err))
			}
		}
	}()

	if err := copyContext(ctx, temp, source); err != nil {
		closeErr := temp.Close()
		temp = nil
		return errors.Join(err, wrapIf(closeErr, "close remote temporary file"))
	}
	if err := contextErr(ctx); err != nil {
		closeErr := temp.Close()
		temp = nil
		return errors.Join(err, wrapIf(closeErr, "close remote temporary file"))
	}
	if err := temp.Close(); err != nil {
		temp = nil
		return fmt.Errorf("close remote temporary file: %w", err)
	}
	temp = nil
	currentSource, err := source.Stat()
	if err != nil {
		return fmt.Errorf("restat local source: %w", err)
	}
	if !sameMetadata(initialSource, currentSource) {
		return fmt.Errorf("local source changed during transfer")
	}
	if err := source.Close(); err != nil {
		return fmt.Errorf("close local source: %w", err)
	}
	if err := verifyRemoteTargetUnchanged(client, remoteDestination, initialTarget, targetExists); err != nil {
		return fmt.Errorf("remote destination changed during transfer: %w", err)
	}
	if err := contextErr(ctx); err != nil {
		return err
	}

	if targetExists {
		if _, ok := client.HasExtension(posixRenameExtension); !ok {
			return fmt.Errorf("server does not support %s; existing destination was preserved", posixRenameExtension)
		}
		if err := client.Chmod(tempPath, initialTarget.Mode().Perm()); err != nil {
			return fmt.Errorf("preserve remote destination permissions: %w", err)
		}
		if err := client.PosixRename(tempPath, remoteDestination); err != nil {
			return fmt.Errorf("replace remote destination: %w", err)
		}
	} else {
		if err := client.Rename(tempPath, remoteDestination); err != nil {
			return fmt.Errorf("install remote destination: %w", err)
		}
	}
	tempExists = false
	return nil
}

// Download copies one remote regular file to a local destination path.
// It writes to an exclusive temporary file beside the destination, then
// installs the completed file after checking the destination again.
func Download(ctx context.Context, client *sftp.Client, remoteSource, localDestination string, overwrite bool) (retErr error) {
	if client == nil {
		return errors.New("SFTP client is nil")
	}
	if err := contextErr(ctx); err != nil {
		return err
	}
	if err := validateRemoteAbsolute(remoteSource); err != nil {
		return fmt.Errorf("remote source: %w", err)
	}
	if err := validateRemoteDirectoryChain(client, path.Dir(remoteSource)); err != nil {
		return fmt.Errorf("remote source parent: %w", err)
	}
	remoteInfo, err := client.Lstat(remoteSource)
	if err != nil {
		return fmt.Errorf("stat remote source: %w", err)
	}
	if remoteInfo.Mode()&os.ModeSymlink != 0 || !remoteInfo.Mode().IsRegular() {
		return fmt.Errorf("remote source is not a regular file")
	}

	if err := validateLocalPath(localDestination); err != nil {
		return fmt.Errorf("local destination path: %w", err)
	}
	localParent := filepath.Dir(localDestination)
	parentInfo, err := os.Lstat(localParent)
	if err != nil {
		return fmt.Errorf("stat local destination parent: %w", err)
	}
	if parentInfo.Mode()&os.ModeSymlink != 0 || !parentInfo.IsDir() {
		return fmt.Errorf("local destination parent is not a real directory")
	}
	initialTarget, targetExists, err := localTargetSnapshot(localDestination)
	if err != nil {
		return fmt.Errorf("inspect local destination: %w", err)
	}
	if targetExists && !overwrite {
		return fmt.Errorf("local destination already exists")
	}

	remoteFile, err := client.Open(remoteSource)
	if err != nil {
		return fmt.Errorf("open remote source: %w", err)
	}
	remoteFileClosed := false
	defer func() {
		if !remoteFileClosed {
			_ = remoteFile.Close()
		}
	}()

	if err := validateLocalPath(localDestination); err != nil {
		return fmt.Errorf("local destination path before temporary file creation: %w", err)
	}
	tempPath := filepath.Join(localParent, "."+filepath.Base(localDestination)+".transfer-"+uuid.NewString()+".tmp")
	temp, err := os.OpenFile(tempPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("create local temporary file: %w", err)
	}
	tempExists := true
	defer func() {
		if tempExists {
			if err := os.Remove(tempPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				retErr = errors.Join(retErr, fmt.Errorf("remove local temporary file %q: %w", tempPath, err))
			}
		}
	}()

	if err := copyContext(ctx, temp, remoteFile); err != nil {
		closeErr := temp.Close()
		temp = nil
		return errors.Join(err, wrapIf(closeErr, "close local temporary file"))
	}
	if err := contextErr(ctx); err != nil {
		closeErr := temp.Close()
		temp = nil
		return errors.Join(err, wrapIf(closeErr, "close local temporary file"))
	}
	if err := temp.Sync(); err != nil {
		closeErr := temp.Close()
		temp = nil
		return errors.Join(fmt.Errorf("sync local temporary file: %w", err), wrapIf(closeErr, "close local temporary file"))
	}
	if err := temp.Close(); err != nil {
		temp = nil
		return fmt.Errorf("close local temporary file: %w", err)
	}
	temp = nil

	currentSource, err := client.Lstat(remoteSource)
	if err != nil {
		return fmt.Errorf("restat remote source: %w", err)
	}
	if currentSource.Mode()&os.ModeSymlink != 0 || !currentSource.Mode().IsRegular() || !sameMetadata(remoteInfo, currentSource) {
		return fmt.Errorf("remote source changed during transfer")
	}
	if err := remoteFile.Close(); err != nil {
		remoteFileClosed = true
		return fmt.Errorf("close remote source: %w", err)
	}
	remoteFileClosed = true
	if err := verifyLocalTargetUnchanged(localDestination, initialTarget, targetExists); err != nil {
		return fmt.Errorf("local destination changed during transfer: %w", err)
	}
	if targetExists {
		if err := os.Chmod(tempPath, initialTarget.Mode().Perm()); err != nil {
			return fmt.Errorf("preserve local destination permissions: %w", err)
		}
	}
	if err := contextErr(ctx); err != nil {
		return err
	}
	if err := validateLocalPath(localDestination); err != nil {
		return fmt.Errorf("local destination path before replacement: %w", err)
	}
	if err := verifyLocalTargetUnchanged(localDestination, initialTarget, targetExists); err != nil {
		return fmt.Errorf("local destination changed before replacement: %w", err)
	}
	if err := replaceLocal(tempPath, localDestination, targetExists); err != nil {
		return fmt.Errorf("install local destination: %w", err)
	}
	tempExists = false
	return nil
}

// validateLocalPath delegates ancestor, symlink/junction, and target-name
// checks to the same mapper used by the application layer. Missing target
// files remain valid because RemoteToLocal is designed for downloads.
func validateLocalPath(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("path is empty")
	}
	absolute, err := filepath.Abs(name)
	if err != nil {
		return err
	}
	abs := filepath.Clean(absolute)
	mapper, err := mapping.New(filepath.Dir(abs), "/")
	if err != nil {
		return err
	}
	_, err = mapper.RemoteToLocal("/" + filepath.Base(abs))
	return err
}

func copyContext(ctx context.Context, dst io.Writer, src io.Reader) error {
	buf := make([]byte, copyChunkSize)
	for {
		if err := contextErr(ctx); err != nil {
			return err
		}
		n, readErr := src.Read(buf)
		if n > 0 {
			written := 0
			for written < n {
				if err := contextErr(ctx); err != nil {
					return err
				}
				m, writeErr := dst.Write(buf[written:n])
				written += m
				if report, ok := ctx.Value(progressKey{}).(func(int64)); ok && m > 0 {
					report(int64(m))
				}
				if writeErr != nil {
					return fmt.Errorf("write transfer data: %w", writeErr)
				}
				if m == 0 {
					return io.ErrShortWrite
				}
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return fmt.Errorf("read transfer data: %w", readErr)
		}
	}
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return errors.New("context is nil")
	}
	return ctx.Err()
}

func validateRemoteAbsolute(name string) error {
	if name == "" || !strings.HasPrefix(name, "/") || strings.ContainsRune(name, '\x00') {
		return fmt.Errorf("path must be absolute and contain no NUL")
	}
	if path.Clean(name) != name || name == "/" || path.Base(name) == "." || path.Base(name) == ".." {
		return fmt.Errorf("path must be a normalized file path")
	}
	return nil
}

func validateRemoteDirectoryChain(client *sftp.Client, dir string) error {
	if dir == "." || !strings.HasPrefix(dir, "/") {
		return fmt.Errorf("directory path must be absolute")
	}
	for current := "/"; ; {
		info, err := client.Lstat(current)
		if err != nil {
			return fmt.Errorf("lstat %q: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("path component %q is not a real directory", current)
		}
		if current == dir {
			return nil
		}
		rel := strings.TrimPrefix(dir, current)
		rel = strings.TrimPrefix(rel, "/")
		part := strings.SplitN(rel, "/", 2)[0]
		if part == "" {
			return fmt.Errorf("invalid directory path %q", dir)
		}
		if current == "/" {
			current += part
		} else {
			current = path.Join(current, part)
		}
	}
}

func sameMetadata(a, b os.FileInfo) bool {
	return a.Mode() == b.Mode() && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

func remoteTargetSnapshot(client *sftp.Client, name string) (os.FileInfo, bool, error) {
	info, err := client.Lstat(name)
	if err != nil {
		if isNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("destination exists and is not a regular file")
	}
	return info, true, nil
}

func localTargetSnapshot(name string) (os.FileInfo, bool, error) {
	info, err := os.Lstat(name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("destination exists and is not a regular file")
	}
	return info, true, nil
}

func verifyRemoteTargetUnchanged(client *sftp.Client, name string, before os.FileInfo, existed bool) error {
	after, exists, err := remoteTargetSnapshot(client, name)
	if err != nil {
		return err
	}
	if exists != existed || (exists && !sameMetadata(before, after)) {
		return fmt.Errorf("target metadata differs from initial snapshot")
	}
	return nil
}

func verifyLocalTargetUnchanged(name string, before os.FileInfo, existed bool) error {
	after, exists, err := localTargetSnapshot(name)
	if err != nil {
		return err
	}
	if exists != existed || (exists && !sameMetadata(before, after)) {
		return fmt.Errorf("target metadata differs from initial snapshot")
	}
	return nil
}

func isNotExist(err error) bool { return errors.Is(err, os.ErrNotExist) }

func wrapIf(err error, context string) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", context, err)
}

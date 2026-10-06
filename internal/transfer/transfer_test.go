package transfer

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pkg/sftp"
)

func TestUploadDownloadRoundTripAndOverwriteDenial(t *testing.T) {
	root := t.TempDir()
	client, closeServer := testClient(t, root)
	defer closeServer()

	content := bytes.Repeat([]byte("binary\x00payload\xff"), 20_000)
	localSource := filepath.Join(root, "source.bin")
	if err := os.WriteFile(localSource, content, 0600); err != nil {
		t.Fatal(err)
	}
	remotePath := remotePath(filepath.Join(root, "remote.bin"))
	if err := Upload(context.Background(), client, localSource, remotePath, false); err != nil {
		t.Fatalf("Upload() error = %v", err)
	}

	deniedSource := filepath.Join(root, "replacement.bin")
	if err := os.WriteFile(deniedSource, []byte("must not replace"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Upload(context.Background(), client, deniedSource, remotePath, false); err == nil {
		t.Fatal("Upload() allowed overwrite without permission")
	}
	expectedRemote := content
	var expectedRemoteMode os.FileMode
	if _, supported := client.HasExtension(posixRenameExtension); supported {
		if err := client.Chmod(remotePath, 0640); err != nil {
			t.Fatalf("set remote destination permissions: %v", err)
		}
		beforeMode, err := client.Lstat(remotePath)
		if err != nil {
			t.Fatal(err)
		}
		expectedRemoteMode = beforeMode.Mode().Perm()
		if err := Upload(context.Background(), client, deniedSource, remotePath, true); err != nil {
			t.Fatalf("Upload() with POSIX rename support: %v", err)
		}
		afterMode, err := client.Lstat(remotePath)
		if err != nil {
			t.Fatal(err)
		}
		if afterMode.Mode().Perm() != expectedRemoteMode {
			t.Fatalf("remote permissions = %04o, want preserved %04o", afterMode.Mode().Perm(), expectedRemoteMode)
		}
		var readErr error
		expectedRemote, readErr = os.ReadFile(deniedSource)
		if readErr != nil {
			t.Fatal(readErr)
		}
	} else if err := Upload(context.Background(), client, deniedSource, remotePath, true); err == nil {
		t.Fatal("Upload() replaced an existing remote file without POSIX rename support")
	}
	remoteContents, err := client.Open(remotePath)
	if err != nil {
		t.Fatal(err)
	}
	remoteBytes, readErr := io.ReadAll(remoteContents)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if err := remoteContents.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(remoteBytes, expectedRemote) {
		t.Fatal("remote destination content does not match the expected overwrite result")
	}

	localDestination := filepath.Join(root, "download.bin")
	if err := Download(context.Background(), client, remotePath, localDestination, false); err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	actual, err := os.ReadFile(localDestination)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, expectedRemote) {
		t.Fatal("downloaded bytes differ from uploaded bytes")
	}
	if err := os.WriteFile(localDestination, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Download(context.Background(), client, remotePath, localDestination, true); err != nil {
		t.Fatalf("Download() overwrite error = %v", err)
	}
	actual, err = os.ReadFile(localDestination)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, expectedRemote) {
		t.Fatal("overwritten download bytes differ from source")
	}
}

func TestRejectRemoteAndLocalSourceSymlinks(t *testing.T) {
	root := t.TempDir()
	client, closeServer := testClient(t, root)
	defer closeServer()

	good := filepath.Join(root, "good")
	if err := os.Mkdir(good, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(good, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := CheckRemotePath(client, remotePath(link)); err == nil {
		t.Fatal("CheckRemotePath() accepted a symlink")
	}
	if err := Download(context.Background(), client, remotePath(filepath.Join(link, "missing")), filepath.Join(root, "out"), false); err == nil {
		t.Fatal("Download() accepted a symlink parent")
	}
	localFile := filepath.Join(root, "local-source")
	if err := os.WriteFile(localFile, []byte("source"), 0600); err != nil {
		t.Fatal(err)
	}
	localLink := filepath.Join(root, "local-link")
	if err := os.Symlink(localFile, localLink); err != nil {
		t.Skipf("file symlinks unavailable: %v", err)
	}
	if err := Upload(context.Background(), client, localLink, remotePath(filepath.Join(root, "should-not-upload")), false); err == nil {
		t.Fatal("Upload() accepted a local source symlink")
	}
}

func TestUploadCancellationCleansTemporaryFile(t *testing.T) {
	root := t.TempDir()
	client, closeServer := testClient(t, root)
	defer closeServer()
	localSource := filepath.Join(root, "source")
	f, err := os.Create(localSource)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(32 * 1024 * 1024); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	target := remotePath(filepath.Join(root, "cancelled"))
	uploadDone := make(chan error, 1)
	go func() { uploadDone <- Upload(ctx, client, localSource, target, false) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		entries, err := os.ReadDir(root)
		if err != nil {
			t.Fatal(err)
		}
		foundTemp := false
		for _, entry := range entries {
			if strings.Contains(entry.Name(), ".transfer-") {
				foundTemp = true
				break
			}
		}
		if foundTemp {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("upload did not create its temporary file")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	if err := <-uploadDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("Upload() error = %v, want context.Canceled", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".transfer-") {
			t.Fatalf("temporary file left behind: %s", entry.Name())
		}
	}
}

func TestUploadPreservesDestinationWithoutPosixRenameExtension(t *testing.T) {
	if err := sftp.SetSFTPExtensions("hardlink@openssh.com", "statvfs@openssh.com"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sftp.SetSFTPExtensions("hardlink@openssh.com", posixRenameExtension, "statvfs@openssh.com"); err != nil {
			t.Errorf("restore SFTP extensions: %v", err)
		}
	}()
	root := t.TempDir()
	client, closeServer := testClient(t, root)
	defer closeServer()

	localSource := filepath.Join(root, "new-source")
	if err := os.WriteFile(localSource, []byte("new content"), 0600); err != nil {
		t.Fatal(err)
	}
	remoteDestination := filepath.Join(root, "existing-destination")
	original := []byte("original content")
	if err := os.WriteFile(remoteDestination, original, 0600); err != nil {
		t.Fatal(err)
	}
	remoteDestination = remotePath(remoteDestination)
	if err := Upload(context.Background(), client, localSource, remoteDestination, true); err == nil {
		t.Fatal("Upload() replaced an existing file without POSIX rename support")
	}
	remoteFile, err := client.Open(remoteDestination)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := io.ReadAll(remoteFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := remoteFile.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, original) {
		t.Fatal("missing-extension overwrite changed the original destination")
	}
}

func testClient(t *testing.T, root string) (*sftp.Client, func()) {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	server, err := sftp.NewServer(serverConn, sftp.WithServerWorkingDirectory(root))
	if err != nil {
		_ = clientConn.Close()
		_ = serverConn.Close()
		t.Fatal(err)
	}
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		_ = server.Serve()
	}()
	client, err := sftp.NewClientPipe(clientConn, clientConn)
	if err != nil {
		_ = clientConn.Close()
		_ = server.Close()
		t.Fatal(err)
	}
	return client, func() {
		_ = client.Close()
		_ = server.Close()
		<-serverDone
	}
}

func remotePath(local string) string {
	name := filepath.ToSlash(local)
	if !strings.HasPrefix(name, "/") {
		name = "/" + name
	}
	return name
}

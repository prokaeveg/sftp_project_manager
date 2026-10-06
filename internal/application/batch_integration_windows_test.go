//go:build windows

package application

import (
	"awesomeProject/internal/config"
	"awesomeProject/internal/transport"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func TestBatchSystemSSHPreviewCopiesFrozenProfileAndSingleSessionExecution(t *testing.T) {
	sshExecutable, err := exec.LookPath("ssh.exe")
	if err != nil {
		t.Skip("Windows OpenSSH unavailable")
	}
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	serverConfig := &ssh.ServerConfig{PublicKeyCallback: func(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if meta.User() == "tester" && bytes.Equal(key.Marshal(), signer.PublicKey().Marshal()) {
			return nil, nil
		}
		return nil, fmt.Errorf("fixture key rejected")
	}}
	serverConfig.AddHostKey(signer)
	fixtureDirectory := t.TempDir()
	identity := filepath.Join(fixtureDirectory, "identity")
	block, err := ssh.MarshalPrivateKey(private, "batch fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(identity, pem.EncodeToMemory(block), 0600); err != nil {
		t.Fatal(err)
	}
	owner, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	acl := exec.Command("icacls.exe", identity, "/inheritance:r", "/grant:r", "*"+owner.Uid+":R")
	acl.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if output, err := acl.CombinedOutput(); err != nil {
		t.Fatalf("fixture private-key ACL: %v, %s", err, output)
	}
	remoteDirectory := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var fixtureWorkers sync.WaitGroup
	var socketMu sync.Mutex
	fixtureClosed := false
	sockets := map[net.Conn]bool{}
	t.Cleanup(func() {
		_ = listener.Close()
		socketMu.Lock()
		fixtureClosed = true
		for socket := range sockets {
			_ = socket.Close()
		}
		socketMu.Unlock()
		fixtureWorkers.Wait()
	})
	var sessions atomic.Int32
	fixtureWorkers.Add(1)
	go func() {
		defer fixtureWorkers.Done()
		for {
			socket, err := listener.Accept()
			if err != nil {
				return
			}
			socketMu.Lock()
			if fixtureClosed {
				socketMu.Unlock()
				_ = socket.Close()
				return
			}
			sockets[socket] = true
			socketMu.Unlock()
			fixtureWorkers.Add(1)
			go func() {
				defer fixtureWorkers.Done()
				defer socket.Close()
				defer func() {
					socketMu.Lock()
					delete(sockets, socket)
					socketMu.Unlock()
				}()
				connection, channels, requests, err := ssh.NewServerConn(socket, serverConfig)
				if err != nil {
					return
				}
				defer connection.Close()
				sessions.Add(1)
				go ssh.DiscardRequests(requests)
				for channelRequest := range channels {
					if channelRequest.ChannelType() != "session" {
						_ = channelRequest.Reject(ssh.UnknownChannelType, "unsupported")
						continue
					}
					channel, requests, err := channelRequest.Accept()
					if err != nil {
						continue
					}
					fixtureWorkers.Add(1)
					go func() {
						defer fixtureWorkers.Done()
						defer channel.Close()
						for request := range requests {
							var subsystem struct{ Name string }
							_ = ssh.Unmarshal(request.Payload, &subsystem)
							if request.Type != "subsystem" || subsystem.Name != "sftp" {
								_ = request.Reply(false, nil)
								continue
							}
							_ = request.Reply(true, nil)
							server, err := sftp.NewServer(channel, sftp.WithServerWorkingDirectory(remoteDirectory))
							if err != nil {
								return
							}
							defer server.Close()
							_ = server.Serve()
							return
						}
					}()
				}
			}()
		}
	}()
	knownHosts := filepath.Join(fixtureDirectory, "known hosts")
	port := listener.Addr().(*net.TCPAddr).Port
	line := knownhosts.Line([]string{"[127.0.0.1]:" + strconv.Itoa(port)}, signer.PublicKey()) + "\n"
	if err := os.WriteFile(knownHosts, []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	profile := config.Profile{Name: "Frozen batch", LocalRoot: t.TempDir(), RemoteRoot: "/" + filepath.ToSlash(remoteDirectory), Connection: transport.Connection{Mode: "manual", Host: "127.0.0.1", User: "tester", Port: port, IdentityFile: identity, KnownHostsFile: knownHosts}, Excludes: []string{"**/.env"}}
	service, err := New(filepath.Join(fixtureDirectory, "app.exe"))
	if err != nil {
		t.Fatal(err)
	}
	service.status.SSHPath = sshExecutable
	t.Cleanup(service.Shutdown)
	if _, err := service.SaveProfile(profile); err != nil {
		t.Fatal(err)
	}
	batchLocalFile(t, profile, "folder/first.txt", "one")
	batchLocalFile(t, profile, "folder/second.txt", "two")
	selection := []string{"folder", "folder/first.txt"}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	preview, err := service.PreviewBatch(ctx, "upload", selection)
	if err != nil {
		t.Fatal(err)
	}
	if sessions.Load() != 1 || preview.FileCount != 2 || len(preview.Items) != 3 {
		t.Fatalf("preview sessions %d, plan %+v", sessions.Load(), preview)
	}
	selection[0] = "other"
	preview.Items[0].RelativePath = "escape"
	preview.Items[1].Destination = "/escape"
	preview.Items[1].SkippedReason = "mutated by caller"
	profile.Excludes[0] = "**"
	if service.pending.batch.preview.Items[0].RelativePath != "folder" || service.pending.profile.Excludes[0] != "**/.env" {
		t.Fatal("caller mutated frozen preview or profile rules")
	}
	// Selecting/saving a different connection keeps the existing plan frozen.
	replacement := profile
	replacement.LocalRoot = t.TempDir()
	replacement.RemoteRoot = "/different"
	replacement.Connection.Host = "example.invalid"
	replacement.Excludes = []string{"**/.env"}
	if _, err := service.SaveProfile(replacement); err != nil {
		t.Fatal(err)
	}
	result, err := service.ExecuteBatch(ctx, preview.Token, "skip", false)
	if err != nil {
		t.Fatal(err)
	}
	if sessions.Load() != 2 {
		t.Fatalf("batch execution connected %d times, want two total sessions", sessions.Load())
	}
	for _, item := range result.Items {
		if item.Status != "completed" {
			t.Fatalf("frozen plan execution: %+v", result)
		}
	}
	for _, name := range []string{"first.txt", "second.txt"} {
		if _, err := os.Stat(filepath.Join(remoteDirectory, "folder", name)); err != nil {
			t.Fatalf("frozen destination missing: %v", err)
		}
	}
	if _, err := service.ExecuteBatch(ctx, preview.Token, "skip", false); err == nil {
		t.Fatal("completed token was reused")
	}
	profile.Excludes = []string{"**/.env"}
	if _, err := service.SaveProfile(profile); err != nil {
		t.Fatal(err)
	}
	preview, err = service.PreviewBatch(ctx, "upload", []string{"folder/first.txt"})
	if err != nil {
		t.Fatal(err)
	}
	profile.Excludes = []string{"**/.env", "folder/**"}
	if _, err := service.SaveProfile(profile); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ExecuteBatch(ctx, preview.Token, "overwrite", false); err == nil || sessions.Load() != 3 {
		t.Fatalf("policy change did not invalidate batch: %v, sessions %d", err, sessions.Load())
	}
}

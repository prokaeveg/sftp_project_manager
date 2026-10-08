//go:build windows

package transport

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func TestWindowsOpenSSHPasswordAskpass(t *testing.T) {
	sshExe, err := exec.LookPath("ssh.exe")
	if err != nil {
		t.Skip("Windows OpenSSH unavailable")
	}
	const password = "pāss; & 'пароль'"
	server, knownHosts := startPasswordSSH(t, password)
	var prompts atomic.Int32
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ctx = WithAuthPrompt(ctx, func(_ context.Context, prompt string) (string, error) {
		if !strings.Contains(strings.ToLower(prompt), "password") {
			t.Errorf("unexpected askpass prompt %q", prompt)
		}
		prompts.Add(1)
		return password, nil
	})
	connection := Connection{Mode: "manual", Host: "127.0.0.1", User: "tester", Port: server.port, KnownHostsFile: knownHosts, AuthMode: "password"}
	session, err := Open(ctx, sshExe, connection)
	if err != nil {
		t.Fatal(err)
	}
	if prompts.Load() != 1 {
		session.Close()
		t.Fatalf("got %d prompts, want 1", prompts.Load())
	}
	session.Close()

	// Password authentication still requires an exact known_hosts entry.
	if err := os.WriteFile(knownHosts, nil, 0600); err != nil {
		t.Fatal(err)
	}
	prompts.Store(0)
	if session, err := Open(ctx, sshExe, connection); err == nil {
		session.Close()
		t.Fatal("unknown host key accepted")
	}
	if prompts.Load() != 0 {
		t.Fatal("password requested before strict host-key verification")
	}
}

func TestWindowsOpenSSHPasswordAskpassWrongAndCancelled(t *testing.T) {
	sshExe, err := exec.LookPath("ssh.exe")
	if err != nil {
		t.Skip("Windows OpenSSH unavailable")
	}
	server, knownHosts := startPasswordSSH(t, "correct")
	connection := Connection{Mode: "manual", Host: "127.0.0.1", User: "tester", Port: server.port, KnownHostsFile: knownHosts, AuthMode: "password"}
	wrongCtx, wrongCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer wrongCancel()
	var prompts atomic.Int32
	wrongCtx = WithAuthPrompt(wrongCtx, func(context.Context, string) (string, error) {
		prompts.Add(1)
		return "wrong", nil
	})
	if session, err := Open(wrongCtx, sshExe, connection); err == nil {
		session.Close()
		t.Fatal("wrong password authenticated")
	}
	if prompts.Load() != 3 {
		t.Fatalf("got %d password prompts, want OpenSSH limit of 3", prompts.Load())
	}

	started := make(chan struct{})
	cancelCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cancelCtx = WithAuthPrompt(cancelCtx, func(ctx context.Context, _ string) (string, error) {
		close(started)
		<-ctx.Done()
		return "", errors.New("cancelled")
	})
	result := make(chan error, 1)
	go func() {
		_, err := Open(cancelCtx, sshExe, connection)
		result <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("password prompt did not start")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled prompt returned %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SSH process did not stop after prompt cancellation")
	}
}

func TestProductionGUIAskpassHelperWithUnicodePath(t *testing.T) {
	productionExe := filepath.Join("..", "..", "build", "bin", "sftp-project-manager.exe")
	if _, err := os.Stat(productionExe); err != nil {
		t.Skip("production Wails executable has not been built")
	}
	copyPath := filepath.Join(t.TempDir(), "Приложение askpass.exe")
	binary, err := os.ReadFile(productionExe)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(copyPath, binary, 0700); err != nil {
		t.Fatal(err)
	}
	const secret = "prod; & пароль"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	broker, err := newAuthBroker(ctx, func(context.Context, string) (string, error) { return secret, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	cmd := exec.CommandContext(ctx, copyPath, "tester@localhost's password:")
	cmd.Env = managedEnvironment(broker.context(copyPath))
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("production GUI helper failed: %v", err)
	}
	if string(output) != secret+"\n" {
		t.Fatalf("production helper stdout differs: %q", output)
	}
}

type passwordSSHServer struct{ port int }

func startPasswordSSH(t *testing.T, expected string) (passwordSSHServer, string) {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	config := &ssh.ServerConfig{PasswordCallback: func(meta ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
		if meta.User() == "tester" && string(password) == expected {
			return nil, nil
		}
		return nil, fmt.Errorf("password rejected")
	}}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	sandbox := t.TempDir()
	known := filepath.Join(sandbox, "known hosts")
	port := listener.Addr().(*net.TCPAddr).Port
	line := knownhosts.Line([]string{"[127.0.0.1]:" + strconv.Itoa(port)}, signer.PublicKey()) + "\n"
	if err := os.WriteFile(known, []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			socket, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer socket.Close()
				conn, channels, requests, err := ssh.NewServerConn(socket, config)
				if err != nil {
					return
				}
				defer conn.Close()
				go ssh.DiscardRequests(requests)
				for request := range channels {
					if request.ChannelType() != "session" {
						_ = request.Reject(ssh.UnknownChannelType, "unsupported")
						continue
					}
					channel, reqs, err := request.Accept()
					if err != nil {
						continue
					}
					go func() {
						defer channel.Close()
						for req := range reqs {
							var subsystem struct{ Name string }
							ssh.Unmarshal(req.Payload, &subsystem)
							if req.Type != "subsystem" || subsystem.Name != "sftp" {
								_ = req.Reply(false, nil)
								continue
							}
							_ = req.Reply(true, nil)
							sftpServer, err := sftp.NewServer(channel, sftp.WithServerWorkingDirectory(sandbox))
							if err == nil {
								_ = sftpServer.Serve()
								_ = sftpServer.Close()
							}
							return
						}
					}()
				}
			}()
		}
	}()
	return passwordSSHServer{port: port}, known
}

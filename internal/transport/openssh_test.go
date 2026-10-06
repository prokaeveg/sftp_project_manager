package transport

import (
	"awesomeProject/internal/transfer"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestArgumentsSafety(t *testing.T) {
	c := Connection{Mode: "manual", Host: "localhost", User: "tester", Port: 22}
	args, err := Arguments(c)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, option := range []string{"BatchMode=yes", "StrictHostKeyChecking=yes", "ProxyJump=none", "ProxyCommand=none"} {
		if !strings.Contains(joined, option) {
			t.Fatal(option)
		}
	}
	c.Host = "-oProxyCommand=bad"
	if _, err := Arguments(c); err == nil {
		t.Fatal("option injection accepted")
	}
	c = Connection{Mode: "ssh_alias", Alias: "*"}
	if _, err := Arguments(c); err == nil {
		t.Fatal("wildcard alias accepted")
	}
	for _, alias := range []string{`bad"alias`, "bad;alias", "bad$alias", "bad%alias", "bad^alias", `bad\alias`, "bad[alias]"} {
		if _, err := Arguments(Connection{Mode: "ssh_alias", Alias: alias}); err == nil {
			t.Fatalf("unsafe alias accepted: %q", alias)
		}
	}
}

func TestSystemSSHIntegration(t *testing.T) {
	executable, err := exec.LookPath("ssh.exe")
	if err != nil {
		t.Skip("Windows OpenSSH unavailable")
	}
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, _ := ssh.NewSignerFromKey(private)
	serverConfig := &ssh.ServerConfig{PublicKeyCallback: func(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if meta.User() == "tester" && bytes.Equal(key.Marshal(), signer.PublicKey().Marshal()) {
			return nil, nil
		}
		return nil, fmt.Errorf("key rejected")
	}}
	serverConfig.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	sandbox := t.TempDir()
	privateFile := filepath.Join(sandbox, "identity")
	block, err := ssh.MarshalPrivateKey(private, "test fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(privateFile, pem.EncodeToMemory(block), 0600); err != nil {
		t.Fatal(err)
	}
	// os.WriteFile(0600) does not restrict inherited Windows ACLs. Only adjust this generated fixture.
	owner, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	acl := exec.Command("icacls.exe", privateFile, "/inheritance:r", "/grant:r", "*"+owner.Uid+":R")
	hideWindow(acl)
	if output, err := acl.CombinedOutput(); err != nil {
		t.Fatalf("fixture key ACL: %v, %s", err, output)
	}
	if err := os.WriteFile(filepath.Join(sandbox, "sample.txt"), []byte("hello"), 0600); err != nil {
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
				conn, channels, requests, err := ssh.NewServerConn(socket, serverConfig)
				if err != nil {
					return
				}
				defer conn.Close()
				go ssh.DiscardRequests(requests)
				for request := range channels {
					if request.ChannelType() != "session" {
						request.Reject(ssh.UnknownChannelType, "unsupported")
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
								req.Reply(false, nil)
								continue
							}
							req.Reply(true, nil)
							server, err := sftp.NewServer(channel, sftp.WithServerWorkingDirectory(sandbox))
							if err != nil {
								return
							}
							defer server.Close()
							server.Serve()
							return
						}
					}()
				}
			}()
		}
	}()
	port := listener.Addr().(*net.TCPAddr).Port
	known := filepath.Join(sandbox, "known hosts")
	line := knownhosts.Line([]string{"[127.0.0.1]:" + strconv.Itoa(port)}, signer.PublicKey()) + "\n"
	if err := os.WriteFile(known, []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	connection := Connection{Mode: "manual", Host: "127.0.0.1", User: "tester", Port: port, KnownHostsFile: known, IdentityFile: privateFile}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := Open(ctx, executable, connection)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := session.Client.ReadDir(".")
	if err != nil {
		session.Close()
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		if entry.Name() == "sample.txt" {
			found = true
		}
	}
	if !found {
		t.Fatal("sample file missing")
	}
	content := bytes.Repeat([]byte{0, 1, 2, 255}, 18000)
	source := filepath.Join(sandbox, "binary-source")
	os.WriteFile(source, content, 0600)
	remote := filepath.ToSlash(filepath.Join(sandbox, "remote-binary"))
	if !strings.HasPrefix(remote, "/") {
		remote = "/" + remote
	}
	if err := transfer.Upload(ctx, session.Client, source, remote, false); err != nil {
		session.Close()
		t.Fatal(err)
	}
	downloaded := filepath.Join(sandbox, "downloaded")
	if err := transfer.Download(ctx, session.Client, remote, downloaded, false); err != nil {
		session.Close()
		t.Fatal(err)
	}
	actual, err := os.ReadFile(downloaded)
	if err != nil || !bytes.Equal(actual, content) {
		session.Close()
		t.Fatal("binary roundtrip differs", err)
	}
	os.WriteFile(source, []byte("replacement denied"), 0600)
	if err := transfer.Upload(ctx, session.Client, source, remote, false); err == nil {
		session.Close()
		t.Fatal("overwrite allowed without consent")
	}
	session.Close()
	session.Close()
	connection.User = "denied"
	if denied, err := Open(ctx, executable, connection); err == nil {
		denied.Close()
		t.Fatal("unauthorized user accepted")
	} else if !strings.Contains(err.Error(), "авторизация") {
		t.Fatal(err)
	}
	connection.User = "tester"
	if err := os.WriteFile(known, []byte(""), 0600); err != nil {
		t.Fatal(err)
	}
	if rejected, err := Open(ctx, executable, connection); err == nil {
		rejected.Close()
		t.Fatal("unknown host accepted")
	} else if !strings.Contains(err.Error(), "ключ сервера") {
		t.Fatal(err)
	}
	_, changedKey, _ := ed25519.GenerateKey(rand.Reader)
	changedSigner, _ := ssh.NewSignerFromKey(changedKey)
	wrong := knownhosts.Line([]string{"[127.0.0.1]:" + strconv.Itoa(port)}, changedSigner.PublicKey()) + "\n"
	os.WriteFile(known, []byte(wrong), 0600)
	if rejected, err := Open(ctx, executable, connection); err == nil {
		rejected.Close()
		t.Fatal("changed host accepted")
	}
	cancelled, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	if rejected, err := Open(cancelled, executable, connection); err == nil {
		rejected.Close()
		t.Fatal("cancelled context accepted")
	}
	if err := os.WriteFile(known, []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	liveCtx, stop := context.WithCancel(context.Background())
	live, err := Open(liveCtx, executable, connection)
	if err != nil {
		stop()
		t.Fatal(err)
	}
	stop()
	live.Close()
	select {
	case <-live.process.Done:
	default:
		t.Fatal("SSH process tree not reaped after cancellation")
	}
	if err := live.process.Wait(); err != nil {
		t.Fatalf("SSH process wait failed: %v", err)
	}
	listener.Close()
	if unreachable, err := Open(ctx, executable, connection); err == nil {
		unreachable.Close()
		t.Fatal("closed server accepted")
	} else if !strings.Contains(err.Error(), "сервер недоступен") {
		t.Fatal(err)
	}
}

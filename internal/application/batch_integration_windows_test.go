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
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
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
					if channelRequest.ChannelType() == "direct-tcpip" {
						var forward struct {
							Host       string
							Port       uint32
							Origin     string
							OriginPort uint32
						}
						if err := ssh.Unmarshal(channelRequest.ExtraData(), &forward); err != nil || forward.Host != "127.0.0.1" || int(forward.Port) != listener.Addr().(*net.TCPAddr).Port {
							_ = channelRequest.Reject(ssh.Prohibited, "fixture forwards only to itself")
							continue
						}
						destination, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
						if err != nil {
							_ = channelRequest.Reject(ssh.ConnectionFailed, "fixture unavailable")
							continue
						}
						channel, requests, err := channelRequest.Accept()
						if err != nil {
							_ = destination.Close()
							continue
						}
						go ssh.DiscardRequests(requests)
						fixtureWorkers.Add(1)
						go func() {
							defer fixtureWorkers.Done()
							copied := make(chan struct{})
							go func() {
								_, _ = io.Copy(channel, destination)
								_ = channel.Close()
								_ = destination.Close()
								close(copied)
							}()
							_, _ = io.Copy(destination, channel)
							_ = channel.Close()
							_ = destination.Close()
							<-copied
						}()
						continue
					}
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
	// Preview a requested server while the tree is set to a different server.
	projectWithServers := cloneProject(service.Status().Settings.Projects[0])
	otherConnection := projectWithServers.Connections[0]
	otherConnection.ID = "other-server"
	otherConnection.Name = "Other server"
	otherConnection.Connection.Host = "example.invalid"
	otherConnection.Mappings = []config.Mapping{{ID: "other-mapping", LocalPath: ".", DeploymentPath: "."}}
	projectWithServers.Connections = append(projectWithServers.Connections, otherConnection)
	if _, err := service.SaveProject(projectWithServers); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SelectProject(projectWithServers.ID, otherConnection.ID, "other-mapping"); err != nil {
		t.Fatal(err)
	}
	preview, err := service.PreviewBatchForServer(ctx, projectWithServers.ID, "legacy-connection", "legacy-mapping", "upload", selection)
	if err != nil {
		t.Fatal(err)
	}
	if service.Status().Settings.ActiveConnectionID != otherConnection.ID || service.pending.profile.Connection.Host != "127.0.0.1" {
		t.Fatal("target preview changed active selection or captured the wrong connection")
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
	// A paused queue can collect plans. Each job opens exactly one session,
	// keeps its destination when the active profile changes, and revalidates
	// metadata after waiting. Retry must use the failed job's original server.
	profile.Excludes = []string{"**/.env"}
	if _, err := service.SaveProfile(profile); err != nil {
		t.Fatal(err)
	}
	service.SetQueuePaused(true)
	batchLocalFile(t, profile, "queued-one.txt", strings.Repeat("bytes", 20000))
	batchLocalFile(t, profile, "queued-two.txt", "before")
	for _, relative := range []string{"queued-one.txt", "queued-two.txt"} {
		queuedPreview, err := service.PreviewBatch(ctx, "upload", []string{relative})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.EnqueueBatch(ctx, queuedPreview.Token, "skip", false); err != nil {
			t.Fatal(err)
		}
	}
	if got := service.GetQueue(); len(got.Jobs) != 2 || got.Jobs[0].Status != "waiting" || sessions.Load() != 5 {
		t.Fatalf("paused queue: %+v, sessions %d", got, sessions.Load())
	}
	batchLocalFile(t, profile, "queued-two.txt", "changed size while waiting")
	if _, err := service.SaveProfile(replacement); err != nil {
		t.Fatal(err)
	}
	service.SetQueuePaused(false)
	waitQueue := func() QueueState {
		t.Helper()
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			state := service.GetQueue()
			active := false
			for _, job := range state.Jobs {
				active = active || job.Status == "waiting" || job.Status == "running"
			}
			if !active {
				return state
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("queue did not finish")
		return QueueState{}
	}
	state := waitQueue()
	if state.Jobs[0].Status != "completed" || state.Jobs[0].TransferredBytes != 100000 || state.Jobs[0].ProcessedItems != 1 || state.Jobs[1].Status != "failed" || sessions.Load() != 7 {
		t.Fatalf("queue results: %+v, sessions %d", state, sessions.Load())
	}
	if data, err := os.ReadFile(filepath.Join(remoteDirectory, "queued-one.txt")); err != nil || len(data) != 100000 {
		t.Fatalf("queued destination: %v, size %d", err, len(data))
	}
	if _, err := os.Stat(filepath.Join(remoteDirectory, "queued-two.txt")); !os.IsNotExist(err) {
		t.Fatalf("stale source was transmitted: %v", err)
	}
	service.SetQueuePaused(true)
	retry, err := service.PreviewQueueRetry(ctx, state.Jobs[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	if retry.FileCount != 1 || retry.Items[0].RelativePath != "queued-two.txt" || !strings.HasPrefix(retry.Items[0].Destination, profile.RemoteRoot) {
		t.Fatalf("retry was redirected: %+v", retry)
	}
	if _, err := service.EnqueueBatch(ctx, retry.Token, "skip", false); err != nil {
		t.Fatal(err)
	}
	service.SetQueuePaused(false)
	state = waitQueue()
	if state.Jobs[2].Status != "completed" || state.Jobs[2].TransferredBytes != int64(len("changed size while waiting")) || sessions.Load() != 9 {
		t.Fatalf("retry results: %+v, sessions %d", state, sessions.Load())
	}
	// Directory browsing uses a service-owned session. Cancelling a finished
	// request must not kill it; another mapping of this server also reuses it.
	if _, err := service.SaveProfile(profile); err != nil {
		t.Fatal(err)
	}
	readCtx, cancelRead := context.WithCancel(ctx)
	start := time.Now()
	if _, err := service.ListRemote(readCtx, "."); err != nil {
		t.Fatal(err)
	}
	cold := time.Since(start)
	cancelRead()
	start = time.Now()
	if _, err := service.ListRemote(ctx, "folder"); err != nil {
		t.Fatal(err)
	}
	warm := time.Since(start)
	if _, err := service.ListRemote(ctx, "."); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ListRemote(ctx, "missing-directory"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing directory error: %v", err)
	}
	if sessions.Load() != 10 || service.GetNavigationStatus().State != "connected" {
		t.Fatalf("directory reads did not reuse session: %d, %+v", sessions.Load(), service.GetNavigationStatus())
	}
	t.Logf("Windows OpenSSH navigation: initial read %s; new folder on retained session %s; 3 reads plus missing-folder error share 1 SSH session", cold, warm)
	project := service.Status().Settings.Projects[0]
	if err := os.Mkdir(filepath.Join(profile.LocalRoot, "alternate"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(remoteDirectory, "alternate"), 0700); err != nil {
		t.Fatal(err)
	}
	project.Connections[0].Mappings = []config.Mapping{{ID: "legacy-mapping", LocalPath: "folder", DeploymentPath: "folder"}, {ID: "alternate-mapping", LocalPath: "alternate", DeploymentPath: "alternate"}}
	if _, err := service.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SelectProject(project.ID, project.Connections[0].ID, "alternate-mapping"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ListRemote(ctx, "."); err != nil {
		t.Fatal(err)
	}
	if sessions.Load() != 10 {
		t.Fatalf("mapping change opened another session: %d", sessions.Load())
	}
	project.Connections[0].Connection.Host = "example.invalid"
	if _, err := service.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	if service.GetNavigationStatus().State != "disconnected" {
		t.Fatal("connection edit retained old session")
	}
	if _, err := service.SaveProfile(profile); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ListRemote(ctx, "."); err != nil {
		t.Fatal(err)
	}
	// Simulate a server-side disconnect while the browsing session is idle.
	socketMu.Lock()
	for socket := range sockets {
		_ = socket.Close()
	}
	socketMu.Unlock()
	disconnectDeadline := time.Now().Add(3 * time.Second)
	for service.GetNavigationStatus().State != "disconnected" && time.Now().Before(disconnectDeadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if service.GetNavigationStatus().State != "disconnected" {
		t.Fatal("server disconnect left stale connected status")
	}
	if _, err := service.ListRemote(ctx, "."); err != nil {
		t.Fatal(err)
	}
	service.DisconnectRemote()
	if service.GetNavigationStatus().State != "disconnected" {
		t.Fatal("explicit disconnect did not clear session")
	}
	if _, err := service.ListRemote(ctx, "."); err != nil {
		t.Fatal(err)
	}
	if sessions.Load() != 13 {
		t.Fatalf("reconnect counts: %d, want13", sessions.Load())
	}
	// A nested deployment maps remote-root-relative browsing to a different
	// local subfolder, while transfers keep mapping-relative plan paths.
	deploymentProject := service.Status().Settings.Projects[0]
	deploymentProject.Connections[0].Mappings = []config.Mapping{{ID: "legacy-mapping", LocalPath: "folder", DeploymentPath: "folder"}}
	if _, err := service.SaveProject(deploymentProject); err != nil {
		t.Fatal(err)
	}
	wantContents, err := os.ReadFile(filepath.Join(remoteDirectory, "folder", "first.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile.LocalRoot, "folder", "first.txt"), []byte("stale local copy"), 0600); err != nil {
		t.Fatal(err)
	}
	deploymentPreview, err := service.PreviewBatchForServer(ctx, deploymentProject.ID, deploymentProject.Connections[0].ID, "legacy-mapping", "download", []string{"first.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if len(deploymentPreview.Items) != 1 || deploymentPreview.Items[0].Source != profile.RemoteRoot+"/folder/first.txt" || service.pending.profile.RemoteRoot != profile.RemoteRoot+"/folder" {
		t.Fatalf("nested deployment preview: %+v", deploymentPreview)
	}
	if _, err := service.ExecuteBatch(ctx, deploymentPreview.Token, "overwrite", false); err != nil {
		t.Fatal(err)
	}
	gotContents, err := os.ReadFile(filepath.Join(profile.LocalRoot, "folder", "first.txt"))
	if err != nil || string(gotContents) != string(wantContents) {
		t.Fatalf("download into mapped local directory: %q, %v", gotContents, err)
	}
	if service.GetNavigationStatus().State != "connected" || sessions.Load() != 15 {
		t.Fatal("nested transfer must retain navigation and use its own preview/transfer sessions")
	}
	// Both trusted proxy forms retain the navigation channel and close their
	// jump/target connections when the user explicitly disconnects.
	proxyConfig := filepath.Join(fixtureDirectory, "navigation-config")
	commonConfig := fmt.Sprintf("Host fixture-jump fixture-proxyjump fixture-proxycommand\n HostName 127.0.0.1\n User tester\n Port %d\n IdentityFile \"%s\"\n UserKnownHostsFile \"%s\"\n BatchMode yes\n StrictHostKeyChecking yes\n ControlMaster no\n ControlPath none\nHost fixture-proxyjump\n ProxyJump fixture-jump\nHost fixture-proxycommand\n ProxyCommand \"%s\" -F \"%s\" -W %%h:%%p fixture-jump\n", port, filepath.ToSlash(identity), filepath.ToSlash(knownHosts), filepath.ToSlash(sshExecutable), filepath.ToSlash(proxyConfig))
	if err := os.WriteFile(proxyConfig, []byte(commonConfig), 0600); err != nil {
		t.Fatal(err)
	}
	configACL := exec.Command("icacls.exe", proxyConfig, "/inheritance:r", "/grant:r", "*"+owner.Uid+":R")
	configACL.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if output, err := configACL.CombinedOutput(); err != nil {
		t.Fatalf("fixture config ACL: %v %s", err, output)
	}
	for _, alias := range []string{"fixture-proxyjump", "fixture-proxycommand"} {
		proxyProfile := profile
		proxyProfile.Connection = transport.Connection{Mode: "ssh_alias", Alias: alias, ConfigPath: proxyConfig}
		before := sessions.Load()
		if _, err := service.SaveProfile(proxyProfile); err != nil {
			t.Fatal(err)
		}
		for _, relative := range []string{".", "folder", "."} {
			if _, err := service.ListRemote(ctx, relative); err != nil {
				t.Fatalf("%s navigation: %v", alias, err)
			}
		}
		if sessions.Load() != before+2 {
			t.Fatalf("%s opened %d SSH connections for 3 reads, want jump+target only", alias, sessions.Load()-before)
		}
		service.DisconnectRemote()
		deadline := time.Now().Add(2 * time.Second)
		for {
			socketMu.Lock()
			remaining := len(sockets)
			socketMu.Unlock()
			if remaining == 0 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s disconnect left %d fixture sockets open", alias, remaining)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	service.Shutdown()
	if service.GetNavigationStatus().State != "disconnected" {
		t.Fatal("shutdown retained navigation")
	}
	if _, err := service.ListRemote(ctx, "."); err == nil {
		t.Fatal("navigation reopened after shutdown")
	}
}

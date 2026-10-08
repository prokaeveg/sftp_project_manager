//go:build windows

package transport

import (
	"awesomeProject/internal/transfer"
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
	"testing"
	"time"
	"unsafe"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
	"golang.org/x/sys/windows"
)

// This suite exercises the Windows OpenSSH client against local SSH servers.
// Every key, config file, known_hosts file, and server is created under t.TempDir.
func TestProxyIntegration(t *testing.T) {
	executable, err := exec.LookPath("ssh.exe")
	if err != nil {
		t.Skip("Windows OpenSSH unavailable")
	}

	root := t.TempDir()
	identity, signer := proxyTestIdentity(t, root)
	targetRoot := filepath.Join(root, "remote")
	if err := os.Mkdir(targetRoot, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetRoot, "listed.txt"), []byte("listing fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	target := newProxyTestServer(t, signer, targetRoot, true)
	jump2 := newProxyTestServer(t, signer, "", false)
	jump1 := newProxyTestServer(t, signer, "", false)
	restricted := newProxyTestServer(t, signer, "", false)
	jump2.allow(target.port())
	jump1.allow(jump2.port(), target.port())

	knownHosts := filepath.Join(root, "known hosts")
	writeProxyKnownHosts(t, knownHosts, jump1, jump2, target, restricted)
	config := filepath.Join(root, "ssh_config")
	proxyConfig := filepath.Join(root, "ssh config")
	helperExecutable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	writeProxyConfig(t, config, executable, helperExecutable, identity, knownHosts, jump1, jump2, target, restricted)
	writeProxyConfig(t, proxyConfig, executable, helperExecutable, identity, knownHosts, jump1, jump2, target, restricted)
	t.Run("ProxyJump multi-hop SFTP and lifecycle", func(t *testing.T) {
		testCtx, testCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer testCancel()
		connection := Connection{Mode: "ssh_alias", Alias: "target-jump", ConfigPath: config, KnownHostsFile: knownHosts}
		session, err := Open(testCtx, executable, connection)
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()
		assertProxyListingAndTransfer(t, testCtx, session, targetRoot, "multi-hop")

		pid := session.process.PID()
		if pid == 0 {
			t.Fatal("managed SSH process did not expose its PID")
		}
		children := waitProxyDescendants(t, pid, 2, 5*time.Second)
		cancelCtx, stop := context.WithCancel(context.Background())
		live, err := Open(cancelCtx, executable, connection)
		if err != nil {
			stop()
			t.Fatal(err)
		}
		defer live.Close()
		livePID := live.process.PID()
		liveChildren := waitProxyDescendants(t, livePID, 2, 5*time.Second)
		stop()
		select {
		case <-live.process.Done:
		case <-time.After(10 * time.Second):
			t.Fatal("context cancellation did not reap the SSH process tree")
		}
		live.Close()
		waitProxyProcessesExited(t, append([]uint32{livePID}, liveChildren...), 5*time.Second)
		session.Close()
		select {
		case <-session.process.Done:
		case <-time.After(10 * time.Second):
			t.Fatal("Session.Close did not reap the SSH process tree")
		}
		waitProxyProcessesExited(t, append([]uint32{pid}, children...), 5*time.Second)
	})

	t.Run("ProxyCommand with quoted Windows paths", func(t *testing.T) {
		testCtx, testCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer testCancel()
		connection := Connection{Mode: "ssh_alias", Alias: "target-command", ConfigPath: proxyConfig, KnownHostsFile: knownHosts}
		session, err := Open(testCtx, executable, connection)
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()
		assertProxyListingAndTransfer(t, testCtx, session, targetRoot, "proxy-command")
		pid := session.process.PID()
		children := waitProxyDescendants(t, pid, 1, 5*time.Second)
		session.Close()
		select {
		case <-session.process.Done:
		case <-time.After(10 * time.Second):
			t.Fatal("Session.Close did not reap the ProxyCommand process tree")
		}
		waitProxyProcessesExited(t, append([]uint32{pid}, children...), 5*time.Second)
	})

	t.Run("unknown and changed hop or target keys are rejected", func(t *testing.T) {
		cases := []struct {
			name  string
			omit  *proxyTestServer
			wrong *proxyTestServer
		}{
			{name: "unknown first hop", omit: jump1},
			{name: "unknown second hop", omit: jump2},
			{name: "unknown target", omit: target},
			{name: "changed first hop", wrong: jump1},
			{name: "changed second hop", wrong: jump2},
			{name: "changed target", wrong: target},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				writeProxyKnownHostsExcept(t, knownHosts, tc.omit, tc.wrong, jump1, jump2, target)
				connection := Connection{Mode: "ssh_alias", Alias: "target-jump", ConfigPath: config, KnownHostsFile: knownHosts}
				caseCtx, caseCancel := context.WithTimeout(context.Background(), 8*time.Second)
				defer caseCancel()
				if session, err := Open(caseCtx, executable, connection); err == nil {
					session.Close()
					t.Fatal("connection succeeded with an untrusted SSH key")
				} else {
					var expected diagnostics
					_, _ = expected.Write([]byte("host key verification failed"))
					if !strings.Contains(err.Error(), expected.message()) {
						t.Fatalf("failure did not identify host-key rejection: %v", err)
					}
				}
			})
		}
		writeProxyKnownHosts(t, knownHosts, jump1, jump2, target, restricted)
	})

	t.Run("unauthorized hop and disallowed forwarding fail", func(t *testing.T) {
		testCtx, testCancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer testCancel()
		connection := Connection{Mode: "ssh_alias", Alias: "target-denied-user", ConfigPath: config, KnownHostsFile: knownHosts}
		if session, err := Open(testCtx, executable, connection); err == nil {
			session.Close()
			t.Fatal("unauthorized jump host was accepted")
		}
		if jump1.authenticationDenied() == 0 {
			t.Fatal("fixture did not observe the rejected jump-host identity")
		}
		connection.Alias = "target-denied-network"
		if session, err := Open(testCtx, executable, connection); err == nil {
			session.Close()
			t.Fatal("jump host forwarded to a destination outside its allowlist")
		}
		if restricted.deniedCount() == 0 {
			t.Fatal("fixture did not observe the rejected forwarding destination")
		}
	})

	t.Run("cancellation while custom proxy waits for handshake", func(t *testing.T) {
		marker := filepath.Join(root, "proxy child started")
		t.Setenv("AWESOME_PROXY_HELPER_MODE", "parent")
		t.Setenv("AWESOME_PROXY_HELPER_MARKER", marker)
		cancelCtx, stop := context.WithCancel(context.Background())
		defer stop()
		connection := Connection{Mode: "ssh_alias", Alias: "target-stall", ConfigPath: config, KnownHostsFile: knownHosts}
		result := make(chan error, 1)
		go func() {
			_, err := Open(cancelCtx, executable, connection)
			result <- err
		}()
		var pids []uint32
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			data, err := os.ReadFile(marker)
			if err == nil {
				fields := strings.Fields(string(data))
				if len(fields) == 2 {
					for _, field := range fields {
						pid, parseErr := strconv.ParseUint(field, 10, 32)
						if parseErr == nil {
							pids = append(pids, uint32(pid))
						}
					}
				}
				if len(pids) == 2 {
					break
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
		if len(pids) != 2 {
			t.Fatal("proxy helper did not prove both processes started")
		}
		for _, pid := range pids {
			assertProxyProcessAlive(t, pid)
		}
		stop()
		select {
		case err := <-result:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("Open did not return the cancellation error: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("cancellation did not finish Open while proxy waited for handshake")
		}
		waitProxyProcessesExited(t, pids, 5*time.Second)
	})
}

// TestProxyCommandStallHelper is re-executed by OpenSSH as a deliberately
// stalled ProxyCommand. The parent announces both PIDs before waiting on its
// stdin, so the test cannot mistake a command that never launched for cleanup.
func TestProxyCommandStallHelper(t *testing.T) {
	switch os.Getenv("AWESOME_PROXY_HELPER_MODE") {
	case "child":
		for {
			time.Sleep(time.Hour)
		}
	case "parent":
		child := exec.Command(os.Args[0], "-test.run=^TestProxyCommandStallHelper$")
		child.Env = append(os.Environ(), "AWESOME_PROXY_HELPER_MODE=child")
		hideWindow(child)
		if err := child.Start(); err != nil {
			t.Fatalf("start proxy helper child: %v", err)
		}
		if err := os.WriteFile(os.Getenv("AWESOME_PROXY_HELPER_MARKER"), []byte(fmt.Sprintf("%d %d", os.Getpid(), child.Process.Pid)), 0600); err != nil {
			_ = child.Process.Kill()
			_ = child.Wait()
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, os.Stdin)
		for {
			time.Sleep(time.Hour)
		}
	}
}

func assertProxyListingAndTransfer(t *testing.T, ctx context.Context, session *Session, remoteRoot, label string) {
	t.Helper()
	remoteBase := filepath.ToSlash(remoteRoot)
	if !strings.HasPrefix(remoteBase, "/") {
		remoteBase = "/" + remoteBase
	}
	entries, err := session.Client.ReadDir(remoteBase)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		if entry.Name() == "listed.txt" {
			found = true
		}
	}
	if !found {
		t.Fatal("remote listing omitted listed.txt")
	}
	content := bytes.Repeat([]byte{0, 1, 2, 128, 255}, 12000)
	local := filepath.Join(filepath.Dir(remoteRoot), label+" source.bin")
	if err := os.WriteFile(local, content, 0600); err != nil {
		t.Fatal(err)
	}
	remote := strings.TrimSuffix(remoteBase, "/") + "/" + label + ".bin"
	if err := transfer.Upload(ctx, session.Client, local, remote, false); err != nil {
		t.Fatal(err)
	}
	downloaded := filepath.Join(filepath.Dir(remoteRoot), label+" downloaded.bin")
	if err := transfer.Download(ctx, session.Client, remote, downloaded, false); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(downloaded)
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("binary transfer roundtrip differs: %v", err)
	}
}

type proxyTestServer struct {
	listener   net.Listener
	signer     ssh.Signer
	root       string
	sessions   bool
	mu         sync.Mutex
	allowed    map[uint32]bool
	denied     []string
	authDenied int
	active     map[*ssh.ServerConn]struct{}
	channels   int
	sockets    map[net.Conn]struct{}
	wg         sync.WaitGroup
	acceptDone chan struct{}
}

func newProxyTestServer(t *testing.T, signer ssh.Signer, root string, sessions bool) *proxyTestServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &proxyTestServer{listener: listener, signer: signer, root: root, sessions: sessions, allowed: map[uint32]bool{}, active: map[*ssh.ServerConn]struct{}{}, sockets: map[net.Conn]struct{}{}, acceptDone: make(chan struct{})}
	config := &ssh.ServerConfig{PublicKeyCallback: func(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if meta.User() == "tester" && bytes.Equal(key.Marshal(), signer.PublicKey().Marshal()) {
			return nil, nil
		}
		s.mu.Lock()
		s.authDenied++
		s.mu.Unlock()
		return nil, fmt.Errorf("key rejected")
	}}
	config.AddHostKey(signer)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer close(s.acceptDone)
		for {
			socket, err := listener.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.sockets[socket] = struct{}{}
			s.mu.Unlock()
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				defer func() {
					s.mu.Lock()
					delete(s.sockets, socket)
					s.mu.Unlock()
					_ = socket.Close()
				}()
				s.serve(socket, config)
			}()
		}
	}()
	t.Cleanup(s.close)
	return s
}

func (s *proxyTestServer) port() uint32 { return uint32(s.listener.Addr().(*net.TCPAddr).Port) }

func (s *proxyTestServer) allow(ports ...uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, port := range ports {
		s.allowed[port] = true
	}
}

func (s *proxyTestServer) authenticationDenied() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.authDenied
}

func (s *proxyTestServer) deniedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.denied)
}

func (s *proxyTestServer) trackConnection(conn *ssh.ServerConn) {
	s.mu.Lock()
	s.active[conn] = struct{}{}
	s.mu.Unlock()
}

func (s *proxyTestServer) untrackConnection(conn *ssh.ServerConn) {
	s.mu.Lock()
	delete(s.active, conn)
	s.mu.Unlock()
}

func (s *proxyTestServer) trackChannel() {
	s.mu.Lock()
	s.channels++
	s.mu.Unlock()
}

func (s *proxyTestServer) untrackChannel() {
	s.mu.Lock()
	s.channels--
	s.mu.Unlock()
}

func (s *proxyTestServer) close() {
	_ = s.listener.Close()
	<-s.acceptDone
	s.mu.Lock()
	for socket := range s.sockets {
		_ = socket.Close()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *proxyTestServer) serve(socket net.Conn, config *ssh.ServerConfig) {
	conn, channels, requests, err := ssh.NewServerConn(socket, config)
	if err != nil {
		return
	}
	s.trackConnection(conn)
	defer conn.Close()
	defer s.untrackConnection(conn)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ssh.DiscardRequests(requests)
	}()
	for request := range channels {
		switch request.ChannelType() {
		case "direct-tcpip":
			s.serveForward(request)
		case "session":
			s.serveSFTP(request)
		default:
			_ = request.Reject(ssh.UnknownChannelType, "unsupported")
		}
	}
}

func (s *proxyTestServer) serveForward(request ssh.NewChannel) {
	var target struct {
		Host       string
		Port       uint32
		OriginHost string
		OriginPort uint32
	}
	if err := ssh.Unmarshal(request.ExtraData(), &target); err != nil {
		_ = request.Reject(ssh.ConnectionFailed, "malformed forwarding request")
		return
	}
	s.mu.Lock()
	allowed := target.Host == "127.0.0.1" && s.allowed[target.Port]
	if !allowed {
		s.denied = append(s.denied, net.JoinHostPort(target.Host, strconv.Itoa(int(target.Port))))
	}
	s.mu.Unlock()
	if !allowed {
		_ = request.Reject(ssh.Prohibited, "destination is outside fixture allowlist")
		return
	}
	remote, err := net.DialTimeout("tcp", net.JoinHostPort(target.Host, strconv.Itoa(int(target.Port))), 2*time.Second)
	if err != nil {
		_ = request.Reject(ssh.ConnectionFailed, "fixture destination unavailable")
		return
	}
	channel, requests, err := request.Accept()
	if err != nil {
		_ = remote.Close()
		return
	}
	s.trackChannel()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer s.untrackChannel()
		defer channel.Close()
		defer remote.Close()
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			ssh.DiscardRequests(requests)
		}()
		finished := make(chan struct{}, 2)
		var copies sync.WaitGroup
		copies.Add(2)
		go func() { defer copies.Done(); _, _ = io.Copy(channel, remote); finished <- struct{}{} }()
		go func() { defer copies.Done(); _, _ = io.Copy(remote, channel); finished <- struct{}{} }()
		<-finished
		_ = channel.Close()
		_ = remote.Close()
		copies.Wait()
	}()
}

func (s *proxyTestServer) serveSFTP(request ssh.NewChannel) {
	if !s.sessions {
		_ = request.Reject(ssh.Prohibited, "sessions are disabled on jump fixtures")
		return
	}
	channel, requests, err := request.Accept()
	if err != nil {
		return
	}
	s.trackChannel()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer s.untrackChannel()
		defer channel.Close()
		for req := range requests {
			var subsystem struct{ Name string }
			if req.Type != "subsystem" || ssh.Unmarshal(req.Payload, &subsystem) != nil || subsystem.Name != "sftp" {
				_ = req.Reply(false, nil)
				continue
			}
			_ = req.Reply(true, nil)
			server, err := sftp.NewServer(channel, sftp.WithServerWorkingDirectory(s.root))
			if err != nil {
				return
			}
			_ = server.Serve()
			_ = server.Close()
			return
		}
	}()
}

func proxyTestIdentity(t *testing.T, root string) (string, ssh.Signer) {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(private, "local proxy integration")
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(root, "identity key")
	if err := os.WriteFile(name, pem.EncodeToMemory(block), 0600); err != nil {
		t.Fatal(err)
	}
	owner, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	acl := exec.Command("icacls.exe", name, "/inheritance:r", "/grant:r", "*"+owner.Uid+":R")
	hideWindow(acl)
	if output, err := acl.CombinedOutput(); err != nil {
		t.Fatalf("fixture key ACL: %v, %s", err, output)
	}
	return name, signer
}

func writeProxyKnownHosts(t *testing.T, path string, servers ...*proxyTestServer) {
	writeProxyKnownHostsExcept(t, path, nil, nil, servers...)
}

func writeProxyKnownHostsExcept(t *testing.T, path string, omit, wrong *proxyTestServer, servers ...*proxyTestServer) {
	t.Helper()
	var lines []string
	for _, server := range servers {
		if server == omit {
			continue
		}
		key := server.signer.PublicKey()
		if server == wrong {
			_, private, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			wrongSigner, err := ssh.NewSignerFromKey(private)
			if err != nil {
				t.Fatal(err)
			}
			key = wrongSigner.PublicKey()
		}
		lines = append(lines, knownhosts.Line([]string{"[127.0.0.1]:" + strconv.Itoa(int(server.port()))}, key))
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
}

func writeProxyConfig(t *testing.T, path, executable, helperExecutable, identity, knownHosts string, jump1, jump2, target, restricted *proxyTestServer) {
	t.Helper()
	quotePath := func(value string) string { return `"` + filepath.ToSlash(value) + `"` }
	var config strings.Builder
	writeHost := func(alias string, server *proxyTestServer, user string) {
		fmt.Fprintf(&config, "Host %s\n HostName 127.0.0.1\n Port %d\n User %s\n IdentityFile %s\n IdentitiesOnly yes\n StrictHostKeyChecking yes\n UserKnownHostsFile %s\n GlobalKnownHostsFile none\n", alias, server.port(), user, quotePath(identity), quotePath(knownHosts))
	}
	writeHost("jump1", jump1, "tester")
	writeHost("jump2", jump2, "tester")
	writeHost("target-jump", target, "tester")
	fmt.Fprintf(&config, " ProxyJump jump1,jump2\n")
	writeHost("target-command", target, "tester")
	fmt.Fprintf(&config, " ProxyCommand %s -F %s -W %%h:%%p jump1\n", quotePath(executable), quotePath(path))
	writeHost("target-denied-user", target, "tester")
	fmt.Fprintf(&config, " ProxyJump denied-hop\n")
	writeHost("denied-hop", jump1, "denied")
	writeHost("target-denied-network", target, "tester")
	fmt.Fprintf(&config, " ProxyJump restricted-hop\n")
	writeHost("restricted-hop", restricted, "tester")
	writeHost("target-stall", target, "tester")
	fmt.Fprintf(&config, " ProxyCommand %s -test.run=^TestProxyCommandStallHelper$\n", quotePath(helperExecutable))
	if err := os.WriteFile(path, []byte(config.String()), 0600); err != nil {
		t.Fatal(err)
	}
}

// Descendant snapshots prove the test reached a live nested-proxy process tree
// before cancellation. Process handles are also kept open while checking exit,
// so PID reuse cannot turn a successful check into a false positive.
func waitProxyDescendants(t *testing.T, root uint32, minimum int, timeout time.Duration) []uint32 {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		children, err := proxyDescendants(root)
		if err == nil && len(children) >= minimum {
			return children
		}
		time.Sleep(50 * time.Millisecond)
	}
	children, _ := proxyDescendants(root)
	t.Fatalf("expected at least %d live proxy descendants under PID %d, found %v", minimum, root, children)
	return nil
}

func proxyDescendants(root uint32) ([]uint32, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snapshot)
	entries := make([]windows.ProcessEntry32, 0, 64)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return nil, err
	}
	for {
		entries = append(entries, entry)
		err := windows.Process32Next(snapshot, &entry)
		if err != nil {
			break
		}
	}
	parents := map[uint32]uint32{}
	for _, item := range entries {
		parents[item.ProcessID] = item.ParentProcessID
	}
	return proxyDescendantIDs(parents, root), nil
}

func proxyDescendantIDs(parents map[uint32]uint32, root uint32) []uint32 {
	var descendants []uint32
	for id := range parents {
		seen := map[uint32]bool{}
		for parent := parents[id]; parent != 0; parent = parents[parent] {
			// Parent PIDs can be reused after the original process exits. A
			// process snapshot can therefore contain an apparent ancestry cycle.
			if seen[parent] {
				break
			}
			seen[parent] = true
			if parent == root {
				descendants = append(descendants, id)
				break
			}
			if _, ok := parents[parent]; !ok {
				break
			}
		}
	}
	return descendants
}

func TestProxyDescendantIDsIgnoresAncestryCycles(t *testing.T) {
	ids := proxyDescendantIDs(map[uint32]uint32{1: 2, 2: 1, 3: 9, 4: 3, 5: 5}, 9)
	found := map[uint32]bool{}
	for _, id := range ids {
		found[id] = true
	}
	if len(ids) != 2 || !found[3] || !found[4] {
		t.Fatalf("descendants = %v", ids)
	}
}

func waitProxyProcessesExited(t *testing.T, pids []uint32, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var handles []windows.Handle
	defer func() {
		for _, handle := range handles {
			_ = windows.CloseHandle(handle)
		}
	}()
	for _, pid := range pids {
		handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, pid)
		if err == nil {
			handles = append(handles, handle)
		}
	}
	for _, handle := range handles {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			t.Fatal("timed out waiting for proxy process descendants")
		}
		ms := uint32(remaining.Milliseconds())
		if ms == 0 {
			ms = 1
		}
		result, err := windows.WaitForSingleObject(handle, ms)
		if err != nil || result != uint32(windows.WAIT_OBJECT_0) {
			t.Fatalf("proxy descendant did not exit: result=%d err=%v", result, err)
		}
	}
}

func assertProxyProcessAlive(t *testing.T, pid uint32) {
	t.Helper()
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, pid)
	if err != nil {
		t.Fatalf("proxy helper PID %d was not live after its start marker: %v", pid, err)
	}
	defer windows.CloseHandle(handle)
	result, err := windows.WaitForSingleObject(handle, 0)
	if err != nil || result != uint32(windows.WAIT_TIMEOUT) {
		t.Fatalf("proxy helper PID %d exited before cancellation: result=%d err=%v", pid, result, err)
	}
}

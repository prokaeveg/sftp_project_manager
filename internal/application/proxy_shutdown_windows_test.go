package application

import (
	"awesomeProject/internal/config"
	"awesomeProject/internal/transport"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestProxyAppProcessHelper(t *testing.T) {
	stage := os.Getenv("SFTP_PROXY_APP_HELPER")
	if stage == "" {
		return
	}
	if stage == "parent" {
		child := exec.Command(os.Args[0], "-test.run=^TestProxyAppProcessHelper$")
		child.Env = append(os.Environ(), "SFTP_PROXY_APP_HELPER=child")
		child.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		if err := child.Start(); err != nil {
			os.Exit(1)
		}
		if err := os.WriteFile(os.Getenv("SFTP_PROXY_APP_PIDS"), []byte(fmt.Sprintf("%d %d", os.Getpid(), child.Process.Pid)), 0600); err != nil {
			os.Exit(1)
		}
	}
	for {
		time.Sleep(time.Hour)
	}
}

func TestShutdownReapsProxyDuringHandshake(t *testing.T) {
	if _, err := exec.LookPath("ssh.exe"); err != nil {
		t.Skip("Windows OpenSSH unavailable")
	}
	dir := t.TempDir()
	pidsFile := filepath.Join(dir, "proxy-pids")
	t.Setenv("SFTP_PROXY_APP_HELPER", "parent")
	t.Setenv("SFTP_PROXY_APP_PIDS", pidsFile)
	conf := filepath.Join(dir, "config")
	command := strings.ReplaceAll(filepath.ToSlash(os.Args[0]), "\"", "\\\"")
	data := "Host stalled\n HostName example.invalid\n ProxyCommand \"" + command + "\" -test.run=^TestProxyAppProcessHelper$\n"
	if err := os.WriteFile(conf, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	owner, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	acl := exec.Command("icacls.exe", conf, "/inheritance:r", "/grant:r", "*"+owner.Uid+":R")
	acl.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if output, err := acl.CombinedOutput(); err != nil {
		t.Fatalf("temporary config ACL: %v, %s", err, output)
	}
	s, err := New(filepath.Join(dir, "app.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveProfile(config.Profile{Name: "Proxy shutdown", LocalRoot: dir, RemoteRoot: "/srv/app", Connection: transport.Connection{Mode: "ssh_alias", Alias: "stalled", ConfigPath: conf}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { _, err := s.ListRemote(ctx, "."); finished <- err }()
	defer s.Shutdown()
	deadline := time.Now().Add(5 * time.Second)
	var dataPIDs []byte
	for {
		dataPIDs, err = os.ReadFile(pidsFile)
		if err == nil && len(strings.Fields(string(dataPIDs))) == 2 {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-finished
			t.Fatal("proxy helper did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	var handles []windows.Handle
	for _, field := range strings.Fields(string(dataPIDs)) {
		pid, err := strconv.ParseUint(field, 10, 32)
		if err != nil {
			t.Fatal(err)
		}
		handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
		if err != nil {
			t.Fatal(err)
		}
		handles = append(handles, handle)
		defer windows.CloseHandle(handle)
	}
	shutdownDone := make(chan struct{})
	go func() { s.Shutdown(); close(shutdownDone) }()
	select {
	case <-shutdownDone:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("Shutdown did not finish")
	}
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancelled handshake, got %v", err)
	}
	for _, handle := range handles {
		state, err := windows.WaitForSingleObject(handle, 5000)
		if err != nil || state != windows.WAIT_OBJECT_0 {
			t.Fatalf("proxy descendant remains after Shutdown: %v, %d", err, state)
		}
	}
}

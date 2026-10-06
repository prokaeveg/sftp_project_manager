package transport

import (
	"context"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestEvaluateSystemOpenSSHConfig(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows OpenSSH evaluation")
	}
	executable, err := exec.LookPath("ssh.exe")
	if err != nil {
		executable, err = exec.LookPath("ssh")
	}
	if err != nil {
		t.Skip("system OpenSSH unavailable")
	}
	root := filepath.Join(t.TempDir(), "config fixture with spaces")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	includePath := filepath.ToSlash(filepath.Join(root, "included config"))
	mainPath := filepath.ToSlash(filepath.Join(root, "main config"))
	marker := filepath.ToSlash(filepath.Join(t.TempDir(), "match-exec-marker"))
	identity := filepath.ToSlash(filepath.Join(root, "identity file"))
	include := "Match exec \"cmd /c echo ran > " + marker + "\"\n" +
		"  User matched-user\n" +
		"Host selected-target\n" +
		"  HostName config.example.invalid\n" +
		"  User config-user\n" +
		"  Port 2207\n" +
		"  IdentityFile \"" + identity + "\"\n" +
		"  ProxyJump jump.example.invalid\n"
	if err := os.WriteFile(includePath, []byte(include), 0600); err != nil {
		t.Fatal(err)
	}
	main := "Include \"" + includePath + "\"\n"
	if err := os.WriteFile(mainPath, []byte(main), 0600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		owner, err := user.Current()
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{includePath, mainPath} {
			acl := exec.Command("icacls.exe", filepath.FromSlash(path), "/inheritance:r", "/grant:r", "*"+owner.Uid+":R")
			hideWindow(acl)
			if output, err := acl.CombinedOutput(); err != nil {
				t.Fatalf("fixture config ACL: %v, %s", err, output)
			}
		}
	}
	connection := Connection{Mode: "ssh_alias", Alias: "selected-target", ConfigPath: mainPath}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	effective, err := Evaluate(ctx, executable, connection)
	if err != nil {
		t.Fatal(err)
	}
	if effective.Hostname != "config.example.invalid" || effective.User != "matched-user" || effective.Port != 2207 {
		t.Fatalf("unexpected effective config: %+v", effective)
	}
	if len(effective.IdentityFiles) == 0 || effective.IdentityFiles[0] != identity || effective.ProxyJump != "jump.example.invalid" || effective.HasProxyCommand {
		t.Fatalf("missing whitelisted config: %+v", effective)
	}
	if effective.ConfigPath != mainPath || effective.Alias != connection.Alias {
		t.Fatalf("wrong provenance: %+v", effective)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("Match exec did not run: %v", err)
	}

	proxyPath := filepath.ToSlash(filepath.Join(root, "proxy config"))
	proxyConfig := "Host selected-target\n  HostName config.example.invalid\n  User config-user\n  ProxyCommand echo private-proxy-command\n"
	if err := os.WriteFile(proxyPath, []byte(proxyConfig), 0600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		acl := exec.Command("icacls.exe", filepath.FromSlash(proxyPath), "/inheritance:r", "/grant:r", "*"+ownerSID()+":R")
		hideWindow(acl)
		if output, err := acl.CombinedOutput(); err != nil {
			t.Fatalf("fixture proxy config ACL: %v, %s", err, output)
		}
	}
	proxyEffective, err := Evaluate(ctx, executable, Connection{Mode: "ssh_alias", Alias: "selected-target", ConfigPath: proxyPath})
	if err != nil {
		t.Fatal(err)
	}
	if !proxyEffective.HasProxyCommand || proxyEffective.ProxyJump != "" {
		t.Fatalf("proxy command was not reduced to its presence flag: %+v", proxyEffective)
	}
}

func ownerSID() string {
	current, _ := user.Current()
	if current == nil {
		return ""
	}
	return current.Uid
}

func TestEvaluateRejectsNonAlias(t *testing.T) {
	if _, err := Evaluate(context.Background(), "ssh", Connection{Mode: "manual"}); err == nil {
		t.Fatal("manual connection accepted")
	}
}

func TestEvaluateWindowsJobReapsChild(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows Job Object behavior")
	}
	root := t.TempDir()
	marker := filepath.Join(root, "child-survived")
	started := filepath.Join(root, "child-started")
	childScript := filepath.Join(root, "child.cmd")
	parentScript := filepath.Join(root, "parent.cmd")
	child := "@echo off\r\necho started > \"" + started + "\"\r\nping -n 3 127.0.0.1 >nul\r\necho alive > \"" + marker + "\"\r\n"
	parent := "@echo off\r\nstart \"\" /b cmd.exe /c \"\"" + childScript + "\"\"\r\nping -n 20 127.0.0.1 >nul\r\n"
	if err := os.WriteFile(childScript, []byte(child), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(parentScript, []byte(parent), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd, err := exec.LookPath("cmd.exe")
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { _, err := runEvaluation(ctx, cmd, []string{"/c", parentScript}); finished <- err }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-finished
			t.Fatal("child did not start; cancellation test is inconclusive")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	err = <-finished
	if err != context.Canceled {
		t.Fatalf("expected cancellation, got %v", err)
	}
	time.Sleep(3 * time.Second)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("nested process survived job cancellation")
	}
}

func TestEvaluatePreCancelledDoesNotStartProcess(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows process runner")
	}
	marker := filepath.Join(t.TempDir(), "must-not-exist")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	command, err := exec.LookPath("cmd.exe")
	if err != nil {
		t.Fatal(err)
	}
	_, err = runEvaluation(ctx, command, []string{"/c", "echo ran > \"" + marker + "\""})
	if err != context.Canceled {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("pre-cancelled process ran: %v", err)
	}
}

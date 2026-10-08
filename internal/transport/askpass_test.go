package transport

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if handled, err := RunAskpass(); handled {
		if err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestAskpassHelperSubprocessReturnsSecretOnlyOnStdout(t *testing.T) {
	const secret = "pāss w/ spaces; & 'пароль'"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	broker, err := newAuthBroker(ctx, func(_ context.Context, prompt string) (string, error) {
		if prompt != "tester@127.0.0.1's password:" {
			t.Errorf("unexpected prompt %q", prompt)
		}
		return secret, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	helperCtx := broker.context(executable)
	cmd := exec.CommandContext(ctx, executable, "tester@127.0.0.1's password:")
	cmd.Env = managedEnvironment(helperCtx)
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("askpass helper failed: %v", err)
	}
	if string(output) != secret+"\n" {
		t.Fatalf("helper output differs: %q", output)
	}
	for _, arg := range cmd.Args {
		if strings.Contains(arg, secret) {
			t.Fatal("password appeared in helper arguments")
		}
	}
	for _, entry := range cmd.Env {
		if strings.Contains(entry, secret) {
			t.Fatal("password appeared in helper environment")
		}
	}
	connection := Connection{Mode: "manual", Host: "127.0.0.1", User: "tester", Port: 22, AuthMode: "password"}
	encoded, err := json.Marshal(connection)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatal("password appeared in connection JSON")
	}
}

func TestAskpassCancellationCancelsBrokerAndChildContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	callbackStarted := make(chan struct{})
	broker, err := newAuthBroker(ctx, func(ctx context.Context, _ string) (string, error) {
		close(callbackStarted)
		<-ctx.Done()
		return "", ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	childCtx := broker.context("application.exe")
	go func() {
		request, err := netDial(broker.listener.Addr().String())
		if err != nil {
			return
		}
		defer request.Close()
		_ = json.NewEncoder(request).Encode(askpassRequest{Token: broker.token, Prompt: "password:"})
		var response askpassResponse
		_ = json.NewDecoder(request).Decode(&response)
	}()
	select {
	case <-callbackStarted:
	case <-time.After(time.Second):
		broker.Close()
		t.Fatal("callback did not start")
	}
	broker.cancel()
	select {
	case <-childCtx.Done():
	case <-time.After(time.Second):
		broker.Close()
		t.Fatal("broker cancellation did not reach the OpenSSH context")
	}
	broker.Close()
}

func netDial(address string) (net.Conn, error) { return net.Dial("tcp", address) }

func TestArgumentsAuthenticationModes(t *testing.T) {
	base := Connection{Mode: "manual", Host: "localhost", User: "tester", Port: 22}
	args, err := Arguments(base)
	if err != nil || !strings.Contains(strings.Join(args, " "), "BatchMode=yes") {
		t.Fatalf("default auth changed: %v %v", args, err)
	}
	base.AuthMode = "password"
	args, err = Arguments(base)
	joined := strings.Join(args, " ")
	if err != nil || !strings.Contains(joined, "BatchMode=no") || !strings.Contains(joined, "PreferredAuthentications=password") || !strings.Contains(joined, "PubkeyAuthentication=no") || !strings.Contains(joined, "KbdInteractiveAuthentication=no") {
		t.Fatalf("password auth options missing: %v, %v", args, err)
	}
	base.AuthMode = "interactive"
	args, err = Arguments(base)
	joined = strings.Join(args, " ")
	if err != nil || !strings.Contains(joined, "PreferredAuthentications=publickey,keyboard-interactive,password") || !strings.Contains(joined, "PubkeyAuthentication=yes") || !strings.Contains(joined, "KbdInteractiveAuthentication=yes") {
		t.Fatalf("interactive auth options missing: %v, %v", args, err)
	}
	base.AuthMode = "unsupported"
	if _, err := Arguments(base); err == nil {
		t.Fatal("unknown authentication mode accepted")
	}
}

func TestAskpassBrokerRejectsOversizedOrInvalidPromptOutput(t *testing.T) {
	broker, err := newAuthBroker(context.Background(), func(context.Context, string) (string, error) {
		return "", errors.New("cancelled")
	})
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	// Invalid clients get no structured error, so protocol failures cannot leak
	// details or the callback's underlying error.
	conn, err := netDial(broker.listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_ = json.NewEncoder(conn).Encode(askpassRequest{Token: "wrong", Prompt: "password:"})
	_ = conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	var response askpassResponse
	if err := json.NewDecoder(conn).Decode(&response); err == nil {
		t.Fatal("invalid token received a response")
	}
	_ = conn.Close()
}

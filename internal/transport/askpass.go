package transport

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	askpassFlagEnv     = "AWESOME_SFTP_INTERNAL_ASKPASS"
	askpassAddrEnv     = "AWESOME_SFTP_ASKPASS_ADDR"
	askpassTokenEnv    = "AWESOME_SFTP_ASKPASS_TOKEN"
	maxAskpassPrompt   = 4096
	maxAskpassRequests = 16
	askpassTimeout     = 2 * time.Minute
)

type authBroker struct {
	listener     net.Listener
	token        string
	ctx          context.Context
	cancel       context.CancelFunc
	callback     func(context.Context, string) (string, error)
	connections  chan struct{}
	requests     chan struct{}
	requestMu    sync.Mutex
	requestCount int
	wg           sync.WaitGroup
	once         sync.Once
}

func newAuthBroker(ctx context.Context, callback func(context.Context, string) (string, error)) (*authBroker, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, errors.New("could not start SSH authentication prompt broker")
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		listener.Close()
		return nil, errors.New("could not initialize SSH authentication prompt broker")
	}
	brokerCtx, cancel := context.WithCancel(ctx)
	b := &authBroker{listener: listener, token: base64.RawURLEncoding.EncodeToString(tokenBytes), ctx: brokerCtx, cancel: cancel, callback: callback, connections: make(chan struct{}, 4), requests: make(chan struct{}, 1)}
	b.wg.Add(1)
	go b.serve()
	return b, nil
}

func (b *authBroker) context(executable string) context.Context {
	return withManagedEnvironment(b.ctx, map[string]string{
		"SSH_ASKPASS":         executable,
		"SSH_ASKPASS_REQUIRE": "force",
		askpassFlagEnv:        "1",
		askpassAddrEnv:        b.listener.Addr().String(),
		askpassTokenEnv:       b.token,
	})
}

func (b *authBroker) serve() {
	defer b.wg.Done()
	go func() {
		<-b.ctx.Done()
		_ = b.listener.Close()
	}()
	for {
		conn, err := b.listener.Accept()
		if err != nil {
			return
		}
		select {
		case b.connections <- struct{}{}:
		case <-b.ctx.Done():
			_ = conn.Close()
			return
		default:
			_ = conn.Close()
			continue
		}
		b.wg.Add(1)
		go func() {
			defer b.wg.Done()
			defer func() { <-b.connections }()
			b.handle(conn)
		}()
	}
}

type askpassRequest struct {
	Token  string `json:"token"`
	Prompt string `json:"prompt"`
}
type askpassResponse struct {
	Value string `json:"value,omitempty"`
	OK    bool   `json:"ok"`
}

func (b *authBroker) handle(conn net.Conn) {
	defer conn.Close()
	stopClose := context.AfterFunc(b.ctx, func() { _ = conn.Close() })
	defer stopClose()
	_ = conn.SetDeadline(time.Now().Add(askpassTimeout))
	var request askpassRequest
	if err := json.NewDecoder(io.LimitReader(conn, maxAskpassPrompt+1024)).Decode(&request); err != nil {
		return
	}
	if subtle.ConstantTimeCompare([]byte(request.Token), []byte(b.token)) != 1 || len(request.Prompt) > maxAskpassPrompt || strings.ContainsAny(request.Prompt, "\x00\r\n") {
		return
	}
	select {
	case b.requests <- struct{}{}:
		defer func() { <-b.requests }()
	case <-b.ctx.Done():
		return
	}
	b.requestMu.Lock()
	b.requestCount++
	count := b.requestCount
	b.requestMu.Unlock()
	if count > maxAskpassRequests {
		return
	}
	callbackCtx, cancel := context.WithTimeout(b.ctx, askpassTimeout)
	defer cancel()
	value, err := b.callback(callbackCtx, request.Prompt)
	if err != nil || b.ctx.Err() != nil || len(value) > maxAskpassPrompt || strings.ContainsAny(value, "\x00\r\n") {
		// A rejected or timed-out prompt must stop authentication at once. Letting
		// ssh retry would turn cancellation into repeated UI prompts.
		b.cancel()
		_ = json.NewEncoder(conn).Encode(askpassResponse{OK: false})
		return
	}
	_ = json.NewEncoder(conn).Encode(askpassResponse{Value: value, OK: true})
}

func (b *authBroker) Close() {
	b.once.Do(func() {
		b.cancel()
		_ = b.listener.Close()
		b.wg.Wait()
	})
}

// RunAskpass handles the private helper invocation dispatched by main before
// Wails starts. It communicates over authenticated loopback IPC; the answer is
// written only to OpenSSH's askpass stdout.
func RunAskpass() (bool, error) {
	if os.Getenv(askpassFlagEnv) != "1" {
		return false, nil
	}
	if len(os.Args) != 2 || len(os.Args[1]) > maxAskpassPrompt {
		return true, errors.New("invalid SSH prompt helper invocation")
	}
	address := os.Getenv(askpassAddrEnv)
	token := os.Getenv(askpassTokenEnv)
	if address == "" || token == "" {
		return true, errors.New("invalid SSH prompt helper configuration")
	}
	host, portText, err := net.SplitHostPort(address)
	if err != nil || host != "127.0.0.1" {
		return true, errors.New("invalid SSH prompt helper endpoint")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return true, errors.New("invalid SSH prompt helper endpoint")
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, portText), 5*time.Second)
	if err != nil {
		return true, errors.New("SSH authentication prompt is unavailable")
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(askpassTimeout))
	if err := json.NewEncoder(conn).Encode(askpassRequest{Token: token, Prompt: os.Args[1]}); err != nil {
		return true, errors.New("SSH authentication prompt failed")
	}
	var response askpassResponse
	if err := json.NewDecoder(io.LimitReader(conn, maxAskpassPrompt+1024)).Decode(&response); err != nil || !response.OK || len(response.Value) > maxAskpassPrompt || strings.ContainsAny(response.Value, "\x00\r\n") {
		return true, errors.New("SSH authentication was cancelled")
	}
	_, err = io.WriteString(os.Stdout, response.Value+"\n")
	if err != nil {
		return true, errors.New("SSH authentication prompt failed")
	}
	return true, nil
}

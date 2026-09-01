package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
)

const (
	webSocketSubprotocol = "herdr-web-client.v1"
	authHeader           = "X-Herdr-Web-Client-Assertion"
	fixtureAudience      = "herdr-web-client-test"
	fixtureSubject       = "fixture-user"
	fixtureEmail         = "fixture@example.test"
	fixtureKeyID         = "herdr-web-client-fixture"
)

type clientEvent struct {
	Kind     string            `json:"kind"`
	PID      int               `json:"pid,omitempty"`
	Argv     []string          `json:"argv,omitempty"`
	CWD      string            `json:"cwd,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
	Cols     int               `json:"cols,omitempty"`
	Rows     int               `json:"rows,omitempty"`
	Data     string            `json:"data,omitempty"`
	ExitCode *int              `json:"exit_code,omitempty"`
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "client" {
		os.Exit(runFakeClient())
	}

	flags := flag.NewFlagSet("herdr-web-client-testfixture", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	target := flags.String("target", "", "exact herdr-web-client artifact to execute")
	if err := flags.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	if strings.TrimSpace(*target) == "" || flags.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: herdr-web-client-testfixture --target /absolute/path/herdr-web-client")
		os.Exit(2)
	}
	if err := runSupervisor(*target); err != nil {
		fmt.Fprintf(os.Stderr, "fixture: %v\n", err)
		os.Exit(1)
	}
}

func runFakeClient() int {
	home := os.Getenv("HOME")
	if home == "" {
		fmt.Fprintln(os.Stderr, "fixture client: HOME is required")
		return 64
	}
	terminal, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fixture client: open controlling terminal: %v\n", err)
		return 64
	}
	_ = terminal.Close()
	path := filepath.Join(home, "client-record.jsonl")
	recorder, err := newClientRecorder(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fixture client: %v\n", err)
		return 64
	}
	defer recorder.Close()

	rows, cols, sizeErr := pty.Getsize(os.Stdin)
	if sizeErr != nil {
		rows, cols = 0, 0
	}
	env := make(map[string]string)
	for _, entry := range os.Environ() {
		name, value, ok := strings.Cut(entry, "=")
		if ok {
			env[name] = value
		}
	}
	_ = recorder.write(clientEvent{
		Kind: "start",
		PID:  os.Getpid(),
		Argv: append([]string(nil), os.Args...),
		CWD:  currentWorkingDirectory(),
		Env:  env,
		Cols: cols,
		Rows: rows,
	})
	_, _ = io.WriteString(os.Stdout, "FIXTURE_PTY_READY\r\n")

	input := make(chan []byte, 8)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		buffer := make([]byte, 3)
		for {
			count, readErr := os.Stdin.Read(buffer)
			if count > 0 {
				payload := append([]byte(nil), buffer[:count]...)
				select {
				case input <- payload:
				case <-readDone:
					return
				}
			}
			if readErr != nil {
				return
			}
		}
	}()

	windowChanges := make(chan os.Signal, 8)
	signal.Notify(windowChanges, syscall.SIGWINCH)
	defer signal.Stop(windowChanges)
	crashTrigger := []byte("fixture-crash")
	exitTrigger := []byte("fixture-exit")
	scrollTrigger := []byte("fixture-scroll")
	triggerTail := make([]byte, 0, len(scrollTrigger)-1)
	for {
		select {
		case payload := <-input:
			if payload == nil {
				code := 0
				_ = recorder.write(clientEvent{Kind: "exit", PID: os.Getpid(), ExitCode: &code})
				return code
			}
			encoded := base64.RawStdEncoding.EncodeToString(payload)
			_ = recorder.write(clientEvent{Kind: "input", PID: os.Getpid(), Data: encoded})
			_, _ = fmt.Fprintf(os.Stdout, "FIXTURE_PTY_INPUT:%s\r\n", encoded)
			triggerWindow := append(triggerTail, payload...)
			if bytes.Contains(triggerWindow, crashTrigger) {
				code := 23
				_ = recorder.write(clientEvent{Kind: "exit", PID: os.Getpid(), ExitCode: &code})
				return code
			}
			if bytes.Contains(triggerWindow, exitTrigger) {
				code := 0
				_ = recorder.write(clientEvent{Kind: "exit", PID: os.Getpid(), ExitCode: &code})
				return code
			}
			if bytes.Contains(triggerWindow, scrollTrigger) {
				for line := range 120 {
					_, _ = fmt.Fprintf(os.Stdout, "FIXTURE_SCROLL_LINE:%03d\r\n", line)
				}
			}
			tailLength := len(scrollTrigger) - 1
			if len(triggerWindow) < tailLength {
				tailLength = len(triggerWindow)
			}
			triggerTail = append(triggerTail[:0], triggerWindow[len(triggerWindow)-tailLength:]...)
		case <-windowChanges:
			newRows, newCols, err := pty.Getsize(os.Stdin)
			if err != nil {
				continue
			}
			_ = recorder.write(clientEvent{Kind: "resize", PID: os.Getpid(), Cols: newCols, Rows: newRows})
			_, _ = fmt.Fprintf(os.Stdout, "FIXTURE_PTY_RESIZE:%dx%d\r\n", newCols, newRows)
		case <-readDone:
			code := 0
			_ = recorder.write(clientEvent{Kind: "exit", PID: os.Getpid(), ExitCode: &code})
			return code
		}
	}
}

func currentWorkingDirectory() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return cwd
}

type clientRecorder struct {
	mu   sync.Mutex
	file *os.File
}

func newClientRecorder(path string) (*clientRecorder, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	return &clientRecorder{file: file}, nil
}

func (r *clientRecorder) write(event clientEvent) error {
	if r == nil || r.file == nil {
		return errors.New("client recorder is closed")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return json.NewEncoder(r.file).Encode(event)
}

func (r *clientRecorder) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return nil
	}
	err := r.file.Close()
	r.file = nil
	return err
}

type fixtureState struct {
	mu sync.Mutex

	origin            string
	issuer            string
	jwksURL           string
	targetPath        string
	privateTargetAddr string
	targetPID         int
	targetExited      bool
	targetExitCode    int
	socketPath        string
	recordPath        string
	caPath            string

	totalRequests        int
	sessionRequests      int
	sessionMarkers       []string
	attachRequests       int
	injectedHeaders      int
	forwardedHeaderVals  []int
	nonceCount           int
	nonces               []string
	websocketConnections int
	websocketClosed      int
	helloMessages        int
	readyMessages        int
	wsHosts              []string
	wsOrigins            []string
	wsProtocols          []string
	websocketHeaderVals  []int
	oidcIssuerRequests   int
	oidcJWKSRequests     int
	snapshotRequests     int
	subscriptionRequests int
	reconcileSnapshots   int
	completionEvents     int
	completionRequested  bool
}

type fixtureStateView struct {
	Origin               string        `json:"origin"`
	Issuer               string        `json:"issuer"`
	JWKSURL              string        `json:"jwks_url"`
	TargetPath           string        `json:"target_path"`
	TargetPID            int           `json:"target_pid"`
	TargetExited         bool          `json:"target_exited"`
	TargetExitCode       int           `json:"target_exit_code"`
	SocketPath           string        `json:"socket_path"`
	RecordPath           string        `json:"record_path"`
	CAPath               string        `json:"ca_path"`
	TotalRequests        int           `json:"total_requests"`
	SessionRequests      int           `json:"session_requests"`
	SessionMarkers       []string      `json:"session_markers"`
	AttachRequests       int           `json:"attach_requests"`
	InjectedHeaders      int           `json:"injected_headers"`
	ForwardedHeaderVals  []int         `json:"forwarded_header_values"`
	NonceCount           int           `json:"nonce_count"`
	Nonces               []string      `json:"nonces"`
	WebSocketConnections int           `json:"websocket_connections"`
	WebSocketClosed      int           `json:"websocket_closed"`
	HelloMessages        int           `json:"hello_messages"`
	ReadyMessages        int           `json:"ready_messages"`
	WSHosts              []string      `json:"websocket_hosts"`
	WSOrigins            []string      `json:"websocket_origins"`
	WSProtocols          []string      `json:"websocket_protocols"`
	WebSocketHeaderVals  []int         `json:"websocket_header_values"`
	OIDCIssuerRequests   int           `json:"oidc_issuer_requests"`
	OIDCJWKSRequests     int           `json:"oidc_jwks_requests"`
	SnapshotRequests     int           `json:"snapshot_requests"`
	SubscriptionRequests int           `json:"subscription_requests"`
	ReconcileSnapshots   int           `json:"reconcile_snapshots"`
	CompletionEvents     int           `json:"completion_events"`
	CompletionRequested  bool          `json:"completion_requested"`
	ClientEvents         []clientEvent `json:"client_events"`
}

func (s *fixtureState) view() fixtureStateView {
	s.mu.Lock()
	view := fixtureStateView{
		Origin:               s.origin,
		Issuer:               s.issuer,
		JWKSURL:              s.jwksURL,
		TargetPath:           s.targetPath,
		TargetPID:            s.targetPID,
		TargetExited:         s.targetExited,
		TargetExitCode:       s.targetExitCode,
		SocketPath:           s.socketPath,
		RecordPath:           s.recordPath,
		CAPath:               s.caPath,
		TotalRequests:        s.totalRequests,
		SessionRequests:      s.sessionRequests,
		SessionMarkers:       append([]string(nil), s.sessionMarkers...),
		AttachRequests:       s.attachRequests,
		InjectedHeaders:      s.injectedHeaders,
		ForwardedHeaderVals:  append([]int(nil), s.forwardedHeaderVals...),
		NonceCount:           s.nonceCount,
		Nonces:               append([]string(nil), s.nonces...),
		WebSocketConnections: s.websocketConnections,
		WebSocketClosed:      s.websocketClosed,
		HelloMessages:        s.helloMessages,
		ReadyMessages:        s.readyMessages,
		WSHosts:              append([]string(nil), s.wsHosts...),
		WSOrigins:            append([]string(nil), s.wsOrigins...),
		WSProtocols:          append([]string(nil), s.wsProtocols...),
		WebSocketHeaderVals:  append([]int(nil), s.websocketHeaderVals...),
		OIDCIssuerRequests:   s.oidcIssuerRequests,
		OIDCJWKSRequests:     s.oidcJWKSRequests,
		SnapshotRequests:     s.snapshotRequests,
		SubscriptionRequests: s.subscriptionRequests,
		ReconcileSnapshots:   s.reconcileSnapshots,
		CompletionEvents:     s.completionEvents,
		CompletionRequested:  s.completionRequested,
	}
	s.mu.Unlock()
	view.ClientEvents = readClientEvents(view.RecordPath)
	return view
}

func (s *fixtureState) noteRequest(path string, forwardedHeaderValues int, sessionMarker string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.totalRequests++
	if path == "/api/session" {
		s.sessionRequests++
		s.sessionMarkers = append(s.sessionMarkers, sessionMarker)
	}
	if path == "/api/attach" {
		s.attachRequests++
	}
	s.injectedHeaders++
	s.forwardedHeaderVals = append(s.forwardedHeaderVals, forwardedHeaderValues)
}

func (s *fixtureState) noteSessionNonce(nonce string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nonceCount++
	if nonce != "" {
		s.nonces = append(s.nonces, nonce)
	}
}

func readClientEvents(path string) []clientEvent {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	var events []clientEvent
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var event clientEvent
		if json.Unmarshal(scanner.Bytes(), &event) == nil {
			events = append(events, event)
		}
	}
	return events
}

type fixture struct {
	ctx    context.Context
	cancel context.CancelFunc
	state  *fixtureState

	rootDir    string
	caPool     *x509.CertPool
	serverCert tls.Certificate
	caPath     string
	proxyToken string

	target     *exec.Cmd
	targetDone chan struct{}

	proxyServer     *http.Server
	issuerServer    *http.Server
	controlServer   *http.Server
	proxyListener   net.Listener
	issuerListener  net.Listener
	controlListener net.Listener
	unixListener    net.Listener

	completionSignal chan struct{}
	subscriptionMu   sync.Mutex
	subscriber       net.Conn
	shutdownOnce     sync.Once
}

type fixtureManifest struct {
	Type       string `json:"type"`
	Origin     string `json:"origin"`
	ControlURL string `json:"control_url"`
	CAPath     string `json:"ca_path"`
	RecordPath string `json:"record_path"`
	TargetPath string `json:"target_path"`
	TargetPID  int    `json:"target_pid"`
	Issuer     string `json:"issuer"`
	JWKSURL    string `json:"jwks_url"`
}

func runSupervisor(targetPath string) error {
	fixture, err := newFixture(targetPath)
	if err != nil {
		return err
	}
	defer fixture.shutdown()

	manifest, err := fixture.start()
	if err != nil {
		return err
	}
	if err := json.NewEncoder(os.Stdout).Encode(manifest); err != nil {
		return fmt.Errorf("write fixture manifest: %w", err)
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	select {
	case <-signals:
		return nil
	case <-fixture.ctx.Done():
		return nil
	}
}

func newFixture(targetPath string) (*fixture, error) {
	absoluteTarget, err := filepath.Abs(targetPath)
	if err != nil {
		return nil, fmt.Errorf("resolve target path: %w", err)
	}
	info, err := os.Stat(absoluteTarget)
	if err != nil {
		return nil, fmt.Errorf("stat target artifact: %w", err)
	}
	if info.IsDir() || info.Mode()&0o111 == 0 {
		return nil, fmt.Errorf("target artifact is not executable: %s", absoluteTarget)
	}
	rootDir, err := os.MkdirTemp("", "herdr-web-client-testfixture-")
	if err != nil {
		return nil, fmt.Errorf("create fixture directory: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	state := &fixtureState{targetPath: absoluteTarget, recordPath: filepath.Join(rootDir, "client-record.jsonl")}
	return &fixture{ctx: ctx, cancel: cancel, state: state, rootDir: rootDir, completionSignal: make(chan struct{}, 1)}, nil
}

func (f *fixture) start() (fixtureManifest, error) {
	ca, cert, pool, err := createCertificateAuthority(f.rootDir)
	if err != nil {
		return fixtureManifest{}, err
	}
	f.caPool = pool
	f.serverCert = cert
	f.caPath = ca
	f.state.mu.Lock()
	f.state.caPath = ca
	f.state.socketPath = filepath.Join(f.rootDir, "herdr.sock")
	f.state.mu.Unlock()

	if err := f.startOIDC(); err != nil {
		f.shutdown()
		return fixtureManifest{}, err
	}
	if err := f.startControl(); err != nil {
		f.shutdown()
		return fixtureManifest{}, err
	}
	if err := f.startProxy(); err != nil {
		f.shutdown()
		return fixtureManifest{}, err
	}
	if err := f.startRPC(); err != nil {
		f.shutdown()
		return fixtureManifest{}, err
	}
	if err := f.startTarget(); err != nil {
		f.shutdown()
		return fixtureManifest{}, err
	}
	if err := f.waitTargetReady(); err != nil {
		f.shutdown()
		return fixtureManifest{}, err
	}

	view := f.state.view()
	return fixtureManifest{
		Type:       "ready",
		Origin:     view.Origin,
		ControlURL: controlURL(f.controlListener),
		CAPath:     view.CAPath,
		RecordPath: view.RecordPath,
		TargetPath: view.TargetPath,
		TargetPID:  view.TargetPID,
		Issuer:     view.Issuer,
		JWKSURL:    view.JWKSURL,
	}, nil
}

func (f *fixture) startOIDC() error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen OIDC issuer: %w", err)
	}
	f.issuerListener = listener
	base := "https://" + listener.Addr().String()
	issuer := base + "/tenant-fixture"
	jwksURL := issuer + "/jwks"
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("generate OIDC signing key: %w", err)
	}
	jwks, err := json.Marshal(map[string]any{"keys": []map[string]string{rsaJWK(&privateKey.PublicKey)}})
	if err != nil {
		return fmt.Errorf("encode OIDC JWKS: %w", err)
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.state.mu.Lock()
		switch r.URL.Path {
		case "/tenant-fixture/jwks":
			f.state.oidcJWKSRequests++
		case "/tenant-fixture/.well-known/openid-configuration", "/.well-known/openid-configuration":
			f.state.oidcIssuerRequests++
		}
		f.state.mu.Unlock()
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		switch r.URL.Path {
		case "/tenant-fixture/jwks":
			_, _ = w.Write(jwks)
		case "/tenant-fixture/.well-known/openid-configuration", "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]string{"issuer": issuer, "jwks_uri": jwksURL})
		default:
			http.NotFound(w, r)
		}
	})
	f.issuerServer = newTLSServer(listener, f.serverCert, handler)
	go func() { _ = f.issuerServer.Serve(tls.NewListener(listener, f.issuerServer.TLSConfig)) }()

	token, err := signJWT(privateKey, issuer, fixtureAudience, fixtureSubject, fixtureEmail)
	if err != nil {
		return err
	}
	f.state.mu.Lock()
	f.state.issuer = issuer
	f.state.jwksURL = jwksURL
	f.state.mu.Unlock()
	f.proxyToken = token
	return nil
}

func (f *fixture) startControl() error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen fixture control: %w", err)
	}
	f.controlListener = listener
	f.controlServer = &http.Server{Handler: http.HandlerFunc(f.controlHandler), ReadHeaderTimeout: 2 * time.Second}
	go func() { _ = f.controlServer.Serve(listener) }()
	return nil
}

func (f *fixture) startProxy() error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen fixture HTTPS proxy: %w", err)
	}
	f.proxyListener = listener
	origin := "https://" + listener.Addr().String()
	f.state.mu.Lock()
	f.state.origin = origin
	f.state.mu.Unlock()
	proxy := &authProxy{fixture: f, authHeader: authHeader}
	f.proxyServer = newTLSServer(listener, f.serverCert, proxy)
	go func() { _ = f.proxyServer.Serve(tls.NewListener(listener, f.proxyServer.TLSConfig)) }()
	return nil
}

func (f *fixture) startRPC() error {
	path := f.state.socketPath
	_ = os.Remove(path)
	listener, err := net.Listen("unix", path)
	if err != nil {
		return fmt.Errorf("listen fixture Herdr socket: %w", err)
	}
	f.unixListener = listener
	go f.acceptRPC()
	return nil
}

func (f *fixture) startTarget() error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("reserve target listener: %w", err)
	}
	targetAddr := listener.Addr().String()
	_ = listener.Close()
	f.state.mu.Lock()
	f.state.privateTargetAddr = targetAddr
	f.state.mu.Unlock()
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve fixture executable: %w", err)
	}
	self, err = filepath.Abs(self)
	if err != nil {
		return fmt.Errorf("resolve fixture executable path: %w", err)
	}
	view := f.state.view()
	env := buildTargetEnvironment(targetAddr, view.Origin, view.Issuer, view.JWKSURL, view.SocketPath, view.RecordPath, view.CAPath, self, f.rootDir)
	cmd := exec.Command(view.TargetPath)
	cmd.Env = env
	cmd.Stdout = io.Discard
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start exact target artifact: %w", err)
	}
	f.target = cmd
	f.targetDone = make(chan struct{})
	f.state.mu.Lock()
	f.state.targetPID = cmd.Process.Pid
	f.state.mu.Unlock()
	go func() {
		_ = cmd.Wait()
		f.state.mu.Lock()
		f.state.targetExited = true
		if cmd.ProcessState != nil {
			f.state.targetExitCode = cmd.ProcessState.ExitCode()
		}
		f.state.mu.Unlock()
		close(f.targetDone)
	}()
	return nil
}

func buildTargetEnvironment(targetAddr, origin, issuer, jwksURL, socketPath, recordPath, caPath, fakePath, workdir string) []string {
	values := map[string]string{
		"HOME":          workdir,
		"USER":          "fixture",
		"LOGNAME":       "fixture",
		"PATH":          filepath.Dir(fakePath) + ":/usr/local/bin:/usr/bin:/bin",
		"TERM":          "xterm-256color",
		"LANG":          "C.UTF-8",
		"NO_PROXY":      "127.0.0.1,localhost",
		"SSL_CERT_FILE": caPath,

		"HERDR_WEB_CLIENT_LISTEN_ADDR":           targetAddr,
		"HERDR_WEB_CLIENT_PUBLIC_ORIGIN":         origin,
		"HERDR_WEB_CLIENT_OIDC_ISSUER":           issuer,
		"HERDR_WEB_CLIENT_OIDC_AUDIENCE":         fixtureAudience,
		"HERDR_WEB_CLIENT_OIDC_ASSERTION_HEADER": authHeader,
		"HERDR_WEB_CLIENT_OIDC_JWKS_URL":         jwksURL,
		"HERDR_WEB_CLIENT_HERDR_PATH":            fakePath,
		"HERDR_WEB_CLIENT_HERDR_WORKDIR":         workdir,
		"HERDR_WEB_CLIENT_HERDR_SOCKET":          socketPath,
	}
	if os.Getenv("HERDR_E2E_SYSTEMD") == "1" {
		values["INVOCATION_ID"] = os.Getenv("INVOCATION_ID")
		values["XDG_RUNTIME_DIR"] = os.Getenv("XDG_RUNTIME_DIR")
		if address := os.Getenv("DBUS_SESSION_BUS_ADDRESS"); address != "" {
			values["DBUS_SESSION_BUS_ADDRESS"] = address
		}
	}

	// Poison legacy names. Construct the removed prefix so source-hygiene
	// scanning can cover this fixture instead of excluding an entire directory.
	legacyPrefix := "HERDR" + "_WEB_"
	values[legacyPrefix+"LISTEN_ADDR"] = "127.0.0.1:1"
	values[legacyPrefix+"PUBLIC_ORIGIN"] = "https://legacy.invalid"
	values[legacyPrefix+"ACCESS_ISSUER"] = "https://legacy.invalid"
	values[legacyPrefix+"ACCESS_AUDIENCE"] = "legacy-audience"
	values[legacyPrefix+"AUDIENCE"] = "legacy-audience"
	result := make([]string, 0, len(values))
	for name, value := range values {
		result = append(result, name+"="+value)
	}
	return result
}

func (f *fixture) waitTargetReady() error {
	view := f.state.view()
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: f.caPool, MinVersion: tls.VersionTLS13}}
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
	defer transport.CloseIdleConnections()
	for {
		request, err := http.NewRequestWithContext(f.ctx, http.MethodGet, "http://"+targetListenAddress(f.state), nil)
		if err == nil {
			request.Host = view.OriginHost()
			request.Header.Set(authHeader, f.proxyToken)
			response, requestErr := client.Do(request)
			if requestErr == nil {
				_, _ = io.Copy(io.Discard, response.Body)
				_ = response.Body.Close()
				if response.StatusCode == http.StatusOK {
					return nil
				}
			}
		}
		select {
		case <-deadline.C:
			return errors.New("target artifact did not become ready at its configured listener")
		case <-f.targetDone:
			return errors.New("target artifact exited before becoming ready")
		case <-f.ctx.Done():
			return f.ctx.Err()
		case <-ticker.C:
		}
	}
}

func targetListenAddress(s *fixtureState) string {
	// The target address is not part of the public manifest. It is captured by
	// the readiness request from the process environment in startTarget.
	// Keep it in the fixture state through the private field below.
	return s.privateTargetAddr
}

func (v fixtureStateView) OriginHost() string {
	parsed, err := url.Parse(v.Origin)
	if err != nil {
		return ""
	}
	return parsed.Host
}

func controlURL(listener net.Listener) string {
	if listener == nil {
		return ""
	}
	return "http://" + listener.Addr().String()
}

func newTLSServer(listener net.Listener, cert tls.Certificate, handler http.Handler) *http.Server {
	return &http.Server{
		Handler:           handler,
		TLSConfig:         &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13},
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
}

func (f *fixture) controlHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	switch r.URL.Path {
	case "/ready", "/state":
		if r.Method != http.MethodGet {
			methodNotAllowedFixture(w, http.MethodGet)
			return
		}
		_ = json.NewEncoder(w).Encode(f.state.view())
	case "/complete":
		if r.Method != http.MethodPost {
			methodNotAllowedFixture(w, http.MethodPost)
			return
		}
		f.requestCompletion()
		_ = json.NewEncoder(w).Encode(f.state.view())
	case "/shutdown":
		if r.Method != http.MethodPost {
			methodNotAllowedFixture(w, http.MethodPost)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "shutting-down"})
		go f.shutdown()
	default:
		http.NotFound(w, r)
	}
}

func methodNotAllowedFixture(w http.ResponseWriter, allowed string) {
	w.Header().Set("Allow", allowed)
	w.WriteHeader(http.StatusMethodNotAllowed)
}

func (f *fixture) requestCompletion() {
	f.state.mu.Lock()
	if f.state.completionRequested {
		f.state.mu.Unlock()
		return
	}
	f.state.completionRequested = true
	f.state.mu.Unlock()
	select {
	case f.completionSignal <- struct{}{}:
	default:
	}
}

func (f *fixture) acceptRPC() {
	for {
		connection, err := f.unixListener.Accept()
		if err != nil {
			if f.ctx.Err() != nil {
				return
			}
			continue
		}
		go f.handleRPC(connection)
	}
}

func (f *fixture) handleRPC(connection net.Conn) {
	defer connection.Close()
	var request struct {
		ID     string          `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.NewDecoder(connection).Decode(&request); err != nil {
		return
	}
	encoder := json.NewEncoder(connection)
	switch request.Method {
	case "session.snapshot":
		if request.ID != "herdr-web-client-snapshot" {
			_ = encoder.Encode(map[string]any{"id": request.ID, "error": map[string]string{"code": "invalid_request", "message": "fixture requires herdr-web-client-snapshot"}})
			return
		}
		f.state.mu.Lock()
		f.state.snapshotRequests++
		if f.state.snapshotRequests >= 2 {
			f.state.reconcileSnapshots++
		}
		done := f.state.completionRequested
		f.state.mu.Unlock()
		_ = encoder.Encode(map[string]any{
			"id": request.ID,
			"result": map[string]any{
				"type": "session_snapshot",
				"snapshot": map[string]any{"panes": []map[string]string{{
					"pane_id":                 "fixture-pane",
					"workspace_id":            "fixture-workspace",
					"agent":                   "fixture-agent",
					"agent_status":            map[bool]string{true: "done", false: "working"}[done],
					"terminal_title":          "Fixture terminal",
					"terminal_title_stripped": "Fixture completed",
				}}},
			},
		})
	case "events.subscribe":
		if request.ID != "herdr-web-client-completions" {
			_ = encoder.Encode(map[string]any{"id": request.ID, "error": map[string]string{"code": "invalid_request", "message": "fixture requires herdr-web-client-completions"}})
			return
		}
		var params struct {
			Subscriptions []struct {
				Type string `json:"type"`
			} `json:"subscriptions"`
		}
		if json.Unmarshal(request.Params, &params) != nil || len(params.Subscriptions) != 1 || params.Subscriptions[0].Type != "pane.updated" {
			_ = encoder.Encode(map[string]any{"id": request.ID, "error": map[string]string{"code": "invalid_request", "message": "fixture requires pane.updated subscription"}})
			return
		}
		f.state.mu.Lock()
		f.state.subscriptionRequests++
		f.state.mu.Unlock()
		if err := encoder.Encode(map[string]any{"id": request.ID, "result": map[string]string{"type": "subscription_started"}}); err != nil {
			return
		}
		f.subscriptionMu.Lock()
		f.subscriber = connection
		f.subscriptionMu.Unlock()
		defer func() {
			f.subscriptionMu.Lock()
			if f.subscriber == connection {
				f.subscriber = nil
			}
			f.subscriptionMu.Unlock()
		}()
		select {
		case <-f.completionSignal:
			f.state.mu.Lock()
			f.state.completionEvents++
			f.state.mu.Unlock()
			_ = encoder.Encode(map[string]any{
				"event": "pane_updated",
				"data": map[string]any{
					"type": "pane_updated",
					"pane": map[string]string{
						"pane_id":                 "fixture-pane",
						"workspace_id":            "fixture-workspace",
						"agent":                   "fixture-agent",
						"agent_status":            "done",
						"terminal_title":          "Fixture terminal",
						"terminal_title_stripped": "Fixture completed",
					},
				},
			})
		case <-f.ctx.Done():
		}
	}
}

func (f *fixture) shutdown() {
	f.shutdownOnce.Do(func() {
		f.cancel()
		f.subscriptionMu.Lock()
		if f.subscriber != nil {
			_ = f.subscriber.Close()
			f.subscriber = nil
		}
		f.subscriptionMu.Unlock()
		if f.unixListener != nil {
			_ = f.unixListener.Close()
		}
		for _, server := range []*http.Server{f.proxyServer, f.issuerServer, f.controlServer} {
			if server != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				_ = server.Shutdown(ctx)
				cancel()
			}
		}
		if f.target != nil && f.target.Process != nil {
			_ = f.target.Process.Signal(syscall.SIGTERM)
			if f.targetDone != nil {
				timer := time.NewTimer(5 * time.Second)
				select {
				case <-f.targetDone:
					timer.Stop()
				case <-timer.C:
					_ = f.target.Process.Kill()
					<-f.targetDone
				}
			}
		}
		_ = os.RemoveAll(f.rootDir)
	})
}

// authProxy terminates fixture HTTPS and forwards to the target's HTTP server.
// Every request has all caller-supplied assertion values removed and exactly
// one freshly signed value injected. WebSocket Host, Origin, and subprotocol
// are copied explicitly rather than reconstructed from the backend URL.
type authProxy struct {
	fixture    *fixture
	authHeader string
}

func (p *authProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if isWebSocketRequest(r) {
		p.serveWebSocket(w, r)
		return
	}
	p.serveHTTP(w, r)
}

func (p *authProxy) serveHTTP(w http.ResponseWriter, r *http.Request) {
	targetURL, _ := url.Parse("http://" + p.fixture.state.privateTargetAddr)
	proxy := &httputil.ReverseProxy{
		Rewrite: func(request *httputil.ProxyRequest) {
			originalHost := request.In.Host
			request.SetURL(targetURL)
			request.Out.Host = originalHost
			request.Out.Header.Del(p.authHeader)
			request.Out.Header.Set(p.authHeader, p.fixture.proxyToken)
		},
		ModifyResponse: func(response *http.Response) error {
			values := response.Request.Header.Values(p.authHeader)
			p.fixture.state.noteRequest(response.Request.URL.Path, len(values), response.Request.Header.Get("X-Herdr-Web-Client-Request"))
			if response.Request.URL.Path == "/api/session" && response.StatusCode == http.StatusOK {
				body, err := io.ReadAll(response.Body)
				if err != nil {
					return err
				}
				_ = response.Body.Close()
				response.Body = io.NopCloser(bytes.NewReader(body))
				var payload struct {
					Nonce string `json:"nonce"`
				}
				if json.Unmarshal(body, &payload) == nil {
					p.fixture.state.noteSessionNonce(payload.Nonce)
				}
			}
			return nil
		},
		ErrorHandler: func(writer http.ResponseWriter, _ *http.Request, err error) {
			http.Error(writer, "fixture proxy: "+err.Error(), http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, r)
}

func (p *authProxy) serveWebSocket(w http.ResponseWriter, r *http.Request) {
	protocolValues := r.Header.Values("Sec-WebSocket-Protocol")
	if len(protocolValues) != 1 || protocolValues[0] != webSocketSubprotocol {
		http.Error(w, "fixture requires Herdr Web Client protocol", http.StatusBadRequest)
		return
	}
	originValues := r.Header.Values("Origin")
	if len(originValues) != 1 {
		http.Error(w, "fixture requires one Origin", http.StatusForbidden)
		return
	}
	upgrader := websocket.Upgrader{
		Subprotocols:      []string{webSocketSubprotocol},
		CheckOrigin:       func(*http.Request) bool { return true },
		EnableCompression: false,
	}
	frontend, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer frontend.Close()

	backendURL := "ws://" + p.fixture.state.privateTargetAddr + r.URL.RequestURI()
	header := make(http.Header)
	header.Del(p.authHeader)
	header.Set(p.authHeader, p.fixture.proxyToken)
	header.Set("Host", r.Host)
	header.Set("Origin", originValues[0])
	p.fixture.state.mu.Lock()
	p.fixture.state.websocketHeaderVals = append(p.fixture.state.websocketHeaderVals, len(header.Values(p.authHeader)))
	p.fixture.state.mu.Unlock()
	dialer := websocket.Dialer{Subprotocols: []string{webSocketSubprotocol}, HandshakeTimeout: 5 * time.Second}
	backend, response, err := dialer.DialContext(r.Context(), backendURL, header)
	if err != nil {
		if response != nil {
			_ = response.Body.Close()
		}
		return
	}
	defer backend.Close()

	p.fixture.state.mu.Lock()
	p.fixture.state.websocketConnections++
	p.fixture.state.wsHosts = append(p.fixture.state.wsHosts, r.Host)
	p.fixture.state.wsOrigins = append(p.fixture.state.wsOrigins, originValues[0])
	p.fixture.state.wsProtocols = append(p.fixture.state.wsProtocols, protocolValues[0])
	p.fixture.state.mu.Unlock()

	done := make(chan struct{}, 2)
	copyMessages := func(source, destination *websocket.Conn, clientToServer bool) {
		defer func() { done <- struct{}{} }()
		for {
			messageType, payload, readErr := source.ReadMessage()
			if readErr != nil {
				return
			}
			if clientToServer {
				var message struct {
					Type string `json:"type"`
				}
				if messageType == websocket.TextMessage && json.Unmarshal(payload, &message) == nil && message.Type == "hello" {
					p.fixture.state.mu.Lock()
					p.fixture.state.helloMessages++
					p.fixture.state.mu.Unlock()
				}
			} else if messageType == websocket.TextMessage {
				var message struct {
					Type string `json:"type"`
				}
				if json.Unmarshal(payload, &message) == nil && message.Type == "ready" {
					p.fixture.state.mu.Lock()
					p.fixture.state.readyMessages++
					p.fixture.state.mu.Unlock()
				}
			}
			if writeErr := destination.WriteMessage(messageType, payload); writeErr != nil {
				return
			}
		}
	}
	go copyMessages(frontend, backend, true)
	go copyMessages(backend, frontend, false)
	<-done
	_ = frontend.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	_ = backend.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	<-done
	p.fixture.state.mu.Lock()
	p.fixture.state.websocketClosed++
	p.fixture.state.mu.Unlock()
}

func isWebSocketRequest(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Connection"), "upgrade") && strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

func createCertificateAuthority(directory string) (string, tls.Certificate, *x509.CertPool, error) {
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", tls.Certificate{}, nil, fmt.Errorf("generate fixture CA key: %w", err)
	}
	serialBytes := make([]byte, 16)
	if _, err := rand.Read(serialBytes); err != nil {
		return "", tls.Certificate{}, nil, err
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          new(big.Int).SetBytes(serialBytes),
		Subject:               pkix.Name{CommonName: "Herdr web client fixture CA"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(2 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return "", tls.Certificate{}, nil, fmt.Errorf("create fixture CA certificate: %w", err)
	}
	caPEM := pemEncode("CERTIFICATE", caDER)
	caPath := filepath.Join(directory, "fixture-ca.pem")
	if err := os.WriteFile(caPath, caPEM, 0o600); err != nil {
		return "", tls.Certificate{}, nil, err
	}

	serverKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", tls.Certificate{}, nil, err
	}
	serverSerial := new(big.Int).SetBytes(append([]byte{1}, serialBytes...))
	serverTemplate := &x509.Certificate{
		SerialNumber: serverSerial,
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(2 * time.Hour),
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caTemplate, &serverKey.PublicKey, caKey)
	if err != nil {
		return "", tls.Certificate{}, nil, fmt.Errorf("create fixture TLS certificate: %w", err)
	}
	certificate, err := tls.X509KeyPair(
		pemEncode("CERTIFICATE", serverDER),
		pemEncode("RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(serverKey)),
	)
	if err != nil {
		return "", tls.Certificate{}, nil, err
	}
	pool := x509.NewCertPool()
	pool.AddCert(mustParseCertificate(caDER))
	return caPath, certificate, pool, nil
}

func pemEncode(kind string, data []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: data})
}

func mustParseCertificate(data []byte) *x509.Certificate {
	certificate, err := x509.ParseCertificate(data)
	if err != nil {
		panic(err)
	}
	return certificate
}

func rsaJWK(key *rsa.PublicKey) map[string]string {
	modulus := key.N.Bytes()
	exponentBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(exponentBytes, uint32(key.E))
	exponentBytes = bytes.TrimLeft(exponentBytes, "\x00")
	return map[string]string{
		"kty": "RSA",
		"use": "sig",
		"alg": "RS256",
		"kid": fixtureKeyID,
		"n":   base64.RawURLEncoding.EncodeToString(modulus),
		"e":   base64.RawURLEncoding.EncodeToString(exponentBytes),
	}
}

func signJWT(key *rsa.PrivateKey, issuer, audience, subject, email string) (string, error) {
	encode := func(value any) (string, error) {
		data, err := json.Marshal(value)
		if err != nil {
			return "", err
		}
		return base64.RawURLEncoding.EncodeToString(data), nil
	}
	header, err := encode(map[string]string{"alg": "RS256", "kid": fixtureKeyID, "typ": "JWT"})
	if err != nil {
		return "", err
	}
	now := time.Now()
	claims, err := encode(map[string]any{
		"iss": issuer, "aud": audience, "sub": subject, "email": email,
		"iat": now.Unix(), "exp": now.Add(30 * time.Minute).Unix(),
	})
	if err != nil {
		return "", err
	}
	message := header + "." + claims
	digest := sha256.Sum256([]byte(message))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return message + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

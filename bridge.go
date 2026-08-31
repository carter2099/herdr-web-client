package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
	"golang.org/x/term"
)

// PTYSession is the process-local terminal used by one WebSocket attachment.
// Wait must reap the child and return its operating-system exit code. A
// negative code denotes termination by signal, matching os.ProcessState.
type PTYSession interface {
	io.Reader
	io.Writer
	io.Closer
	Resize(cols, rows int) error
	Wait() (int, error)
}

// Launcher starts exactly one isolated client process for an attachment.
type Launcher interface {
	Start(ctx context.Context, cols, rows int) (PTYSession, error)
}

const attachmentExecArgument = "__herdr-web-client-attachment-exec"

// PTYLauncher starts one configured Herdr client without a shell.
type PTYLauncher struct {
	Path string
	Dir  string
	Env  []string
}

func NewPTYLauncher(path, dir string) *PTYLauncher {
	defaults := DefaultConfig()
	if path == "" {
		path = defaults.HerdrPath
	}
	if dir == "" {
		dir = defaults.HerdrWorkdir
	}
	return &PTYLauncher{Path: path, Dir: dir}
}

func (l *PTYLauncher) Start(ctx context.Context, cols, rows int) (PTYSession, error) {
	if l == nil {
		return nil, errors.New("nil PTY launcher")
	}
	if ctx == nil {
		return nil, errors.New("nil PTY context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	defaults := DefaultConfig()
	path, dir := l.Path, l.Dir
	if path == "" {
		path = defaults.HerdrPath
	}
	if dir == "" {
		dir = defaults.HerdrWorkdir
	}
	dimensions := (Dimensions{Cols: cols, Rows: rows}).normalized()
	sourceEnv := l.Env
	if sourceEnv == nil {
		sourceEnv = os.Environ()
	}
	clientEnv := buildClientEnv(sourceEnv)

	var (
		cmd        *exec.Cmd
		unitName   string
		controlEnv []string
	)
	if os.Getenv("INVOCATION_ID") != "" {
		controlEnv = buildSystemdControlEnv(os.Environ())
		parentUnit, err := currentSystemdUnit(ctx, controlEnv)
		if err != nil {
			return nil, err
		}
		unitName, err = newAttachmentUnitName()
		if err != nil {
			return nil, err
		}
		executable, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("resolve attachment launcher: %w", err)
		}
		runtimeDir, found := environmentValue(controlEnv, "XDG_RUNTIME_DIR")
		if !found || validateAbsolutePath("XDG runtime directory", runtimeDir) != nil {
			return nil, errors.New("XDG_RUNTIME_DIR must be an absolute path under systemd")
		}
		maskedDevicePaths, err := devicePathsToMask("/dev")
		if err != nil {
			return nil, err
		}
		herdrStateDir := filepath.Join(runtimeHomeDir(), ".config", "herdr")
		cmd = exec.CommandContext(ctx, "/usr/bin/systemd-run", transientUnitArguments(unitName, parentUnit, executable, path, dir, herdrStateDir, runtimeDir, maskedDevicePaths, clientEnv)...)
		cmd.Env = controlEnv
	} else {
		cmd = exec.CommandContext(ctx, path, "client")
		cmd.Dir = dir
		cmd.Env = clientEnv
	}

	file, err := pty.StartWithSize(cmd, &pty.Winsize{
		Cols: uint16(dimensions.Cols),
		Rows: uint16(dimensions.Rows),
	})
	if err != nil {
		return nil, fmt.Errorf("start Herdr client: %w", err)
	}
	session := &ptySession{
		file:       file,
		cmd:        cmd,
		unitName:   unitName,
		controlEnv: controlEnv,
	}
	if _, err := term.MakeRaw(int(file.Fd())); err != nil {
		_ = session.Close()
		_, _ = session.Wait()
		return nil, fmt.Errorf("set Herdr PTY raw mode: %w", err)
	}
	if unitName != "" {
		if err := waitForTransientUnit(ctx, unitName, controlEnv); err != nil {
			diagnostic := availablePTYDiagnostic(file)
			_ = session.Close()
			_, _ = session.Wait()
			if diagnostic != "" {
				err = fmt.Errorf("%w: %s", err, diagnostic)
			}
			return nil, err
		}
	}
	return session, nil
}

func availablePTYDiagnostic(file *os.File) string {
	if file == nil || syscall.SetNonblock(int(file.Fd()), true) != nil {
		return ""
	}
	defer func() { _ = syscall.SetNonblock(int(file.Fd()), false) }()
	buffer := make([]byte, 2048)
	count, _ := file.Read(buffer)
	if count == 0 {
		return ""
	}
	sanitized := strings.Map(func(character rune) rune {
		switch {
		case character == '\n' || character == '\t':
			return character
		case character >= 32 && character <= 126:
			return character
		default:
			return '?'
		}
	}, string(buffer[:count]))
	return strings.TrimSpace(sanitized)
}

var clientEnvironmentAllowlist = []string{
	"HOME",
	"USER",
	"LOGNAME",
	"PATH",
	"TERM",
	"COLORTERM",
	"LANG",
	"LC_ALL",
	"LC_CTYPE",
	"LC_MESSAGES",
	"LC_MONETARY",
	"LC_NUMERIC",
	"LC_TIME",
	"XDG_RUNTIME_DIR",
}

func clientEnvironmentDefaults(source []string) map[string]string {
	defaults := make(map[string]string, 6)
	home := runtimeHomeDir()
	currentUser, _ := user.Current()
	if home != "" {
		defaults["HOME"] = home
	}
	if currentUser != nil {
		defaults["USER"] = currentUser.Username
		defaults["LOGNAME"] = currentUser.Username
		if defaults["HOME"] == "" && filepath.IsAbs(currentUser.HomeDir) {
			defaults["HOME"] = filepath.Clean(currentUser.HomeDir)
		}
	}
	if path := os.Getenv("PATH"); path != "" {
		defaults["PATH"] = path
	}
	if term := os.Getenv("TERM"); term != "" {
		defaults["TERM"] = term
	}
	if lang := os.Getenv("LANG"); lang != "" {
		defaults["LANG"] = lang
	}
	if _, found := environmentValue(source, "HOME"); found {
		delete(defaults, "HOME")
	}
	if _, found := environmentValue(source, "USER"); found {
		delete(defaults, "USER")
	}
	if _, found := environmentValue(source, "LOGNAME"); found {
		delete(defaults, "LOGNAME")
	}
	if _, found := environmentValue(source, "PATH"); found {
		delete(defaults, "PATH")
	}
	if _, found := environmentValue(source, "TERM"); found {
		delete(defaults, "TERM")
	}
	if _, found := environmentValue(source, "LANG"); found {
		delete(defaults, "LANG")
	}
	if defaults["TERM"] == "" {
		defaults["TERM"] = "xterm-256color"
	}
	if defaults["LANG"] == "" {
		defaults["LANG"] = "C.UTF-8"
	}
	return defaults
}

func environmentValue(source []string, name string) (string, bool) {
	for _, entry := range source {
		key, value, found := strings.Cut(entry, "=")
		if found && key == name {
			return value, true
		}
	}
	return "", false
}

// buildClientEnv makes the child environment explicit. In particular,
// HERDR_ENV and all credentials or proxy settings inherited by the service
// are excluded rather than relying on exec's ambient environment behavior.
func buildClientEnv(source []string) []string {
	values := make(map[string]string, len(clientEnvironmentAllowlist))
	for _, entry := range source {
		name, value, found := strings.Cut(entry, "=")
		if !found || name == "" || name == "HERDR_ENV" {
			continue
		}
		for _, allowed := range clientEnvironmentAllowlist {
			if name == allowed {
				values[name] = value
				break
			}
		}
	}
	defaults := clientEnvironmentDefaults(source)
	result := make([]string, 0, len(clientEnvironmentAllowlist))
	for _, name := range clientEnvironmentAllowlist {
		value, ok := values[name]
		if !ok {
			value, ok = defaults[name]
		}
		if ok {
			result = append(result, name+"="+value)
		}
	}
	return result
}

func runAttachmentClient(arguments []string) error {
	if len(arguments) == 0 {
		return errors.New("attachment executable path is required")
	}
	path := arguments[0]
	if err := validateAbsolutePath("Herdr path", path); err != nil {
		return err
	}
	clientEnv := arguments[1:]
	for _, entry := range clientEnv {
		name, _, found := strings.Cut(entry, "=")
		if !found {
			return fmt.Errorf("invalid attachment environment entry %q", entry)
		}
		allowed := false
		for _, candidate := range clientEnvironmentAllowlist {
			if name == candidate {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("attachment environment name is not allowed: %q", name)
		}
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return errors.New("attachment standard input is not a terminal")
	}
	if _, err := term.MakeRaw(int(os.Stdin.Fd())); err != nil {
		return fmt.Errorf("set attachment terminal raw mode: %w", err)
	}
	return syscall.Exec(path, []string{path, "client"}, clientEnv)
}

var systemdControlEnvironmentAllowlist = []string{
	"HOME",
	"USER",
	"LOGNAME",
	"PATH",
	"LANG",
	"LC_ALL",
	"LC_CTYPE",
	"XDG_RUNTIME_DIR",
	"DBUS_SESSION_BUS_ADDRESS",
}

func buildSystemdControlEnv(source []string) []string {
	values := make(map[string]string, len(systemdControlEnvironmentAllowlist))
	for _, entry := range source {
		name, value, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		for _, allowed := range systemdControlEnvironmentAllowlist {
			if name == allowed {
				values[name] = value
				break
			}
		}
	}
	result := make([]string, 0, len(values))
	for _, name := range systemdControlEnvironmentAllowlist {
		if value, ok := values[name]; ok {
			result = append(result, name+"="+value)
		}
	}
	return result
}

func currentSystemdUnit(ctx context.Context, controlEnv []string) (string, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	command := exec.CommandContext(
		probeCtx,
		"/usr/bin/systemctl",
		"--user",
		"whoami",
		strconv.Itoa(os.Getpid()),
	)
	command.Env = controlEnv
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("resolve parent systemd unit: %w", err)
	}
	unitName := strings.TrimSpace(string(output))
	if len(unitName) == 0 || len(unitName) > 255 || !strings.HasSuffix(unitName, ".service") {
		return "", fmt.Errorf("resolve parent systemd unit: invalid service name %q", unitName)
	}
	for _, character := range unitName {
		switch {
		case character >= 'a' && character <= 'z':
		case character >= 'A' && character <= 'Z':
		case character >= '0' && character <= '9':
		case strings.ContainsRune("-_.:@\\", character):
		default:
			return "", fmt.Errorf("resolve parent systemd unit: invalid service name %q", unitName)
		}
	}
	return unitName, nil
}

func newAttachmentUnitName() (string, error) {
	var suffix [16]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", fmt.Errorf("generate attachment unit name: %w", err)
	}
	return "herdr-web-client-attachment-" + hex.EncodeToString(suffix[:]) + ".service", nil
}

func quoteSystemdPath(value string) string {
	var quoted strings.Builder
	quoted.Grow(len(value) + 2)
	quoted.WriteByte('"')
	for index := 0; index < len(value); index++ {
		character := value[index]
		switch {
		case character == '\\' || character == '"':
			quoted.WriteByte('\\')
			quoted.WriteByte(character)
		case character == '%':
			quoted.WriteString("%%")
		case character < 32 || character == 127:
			_, _ = fmt.Fprintf(&quoted, "\\x%02x", character)
		default:
			quoted.WriteByte(character)
		}
	}
	quoted.WriteByte('"')
	return quoted.String()
}

var attachmentPseudoDeviceNames = map[string]struct{}{
	"fd":      {},
	"full":    {},
	"null":    {},
	"ptmx":    {},
	"pts":     {},
	"random":  {},
	"shm":     {},
	"stderr":  {},
	"stdin":   {},
	"stdout":  {},
	"tty":     {},
	"urandom": {},
	"zero":    {},
}

func devicePathsToMask(deviceDirectory string) ([]string, error) {
	entries, err := os.ReadDir(deviceDirectory)
	if err != nil {
		return nil, fmt.Errorf("enumerate device paths: %w", err)
	}

	masked := make([]string, 0, len(entries))
	for _, entry := range entries {
		if _, allowed := attachmentPseudoDeviceNames[entry.Name()]; allowed {
			continue
		}
		masked = append(masked, filepath.Join(deviceDirectory, entry.Name()))
	}
	return masked, nil
}

func inaccessiblePathProperty(runtimeDir string, maskedDevicePaths []string) string {
	paths := make([]string, 0, len(maskedDevicePaths)+2)
	paths = append(paths,
		filepath.Join(runtimeDir, "bus"),
		filepath.Join(runtimeDir, "systemd", "private"),
	)
	paths = append(paths, maskedDevicePaths...)

	quoted := make([]string, 0, len(paths))
	for _, path := range paths {
		quoted = append(quoted, quoteSystemdPath("-"+path))
	}
	return strings.Join(quoted, " ")
}

func transientUnitArguments(unitName, parentUnit, executable, path, dir, herdrStateDir, runtimeDir string, maskedDevicePaths, clientEnv []string) []string {
	arguments := []string{
		"--user",
		"--quiet",
		"--wait",
		"--pty",
		"--collect",
		"--no-ask-password",
		"--service-type=exec",
		"--expand-environment=no",
		"--unit=" + unitName,
		"--working-directory=" + dir,
		"--property=BindsTo=" + parentUnit,
		"--property=After=" + parentUnit,
		"--property=NoNewPrivileges=yes",
		"--property=PrivateTmp=yes",
		"--property=PrivateMounts=yes",
		"--property=ProtectSystem=strict",
		"--property=ProtectHome=read-only",
		"--property=ReadWritePaths=" + quoteSystemdPath(herdrStateDir),
		"--property=InaccessiblePaths=" + inaccessiblePathProperty(runtimeDir, maskedDevicePaths),
		"--property=ProtectKernelTunables=yes",
		"--property=ProtectControlGroups=yes",
		"--property=RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6",
		"--property=RestrictNamespaces=yes",
		"--property=RestrictRealtime=yes",
		"--property=RestrictSUIDSGID=yes",
		"--property=LockPersonality=yes",
		"--property=SystemCallArchitectures=native",
		"--property=KillMode=control-group",
		"--property=KillSignal=SIGKILL",
		"--property=FinalKillSignal=SIGKILL",
		"--property=SendSIGKILL=yes",
		"--property=TimeoutStopSec=1s",
		"--property=TasksMax=64",
		"--property=MemoryMax=512M",
		"--property=CPUQuota=200%",
		"--property=LimitNOFILE=4096",
		"--property=UMask=0077",
		"--",
		executable,
		attachmentExecArgument,
		path,
	}
	arguments = append(arguments, clientEnv...)
	return arguments
}

func transientUnitState(ctx context.Context, unitName string, controlEnv []string) (string, string, error) {
	command := exec.CommandContext(
		ctx,
		"/usr/bin/systemctl",
		"--user",
		"show",
		"--no-pager",
		"--property=LoadState",
		"--property=ActiveState",
		unitName,
	)
	command.Env = controlEnv
	output, err := command.CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("inspect transient attachment unit: %w", err)
	}
	var loadState, activeState string
	for _, line := range strings.Split(string(output), "\n") {
		name, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		switch name {
		case "LoadState":
			loadState = value
		case "ActiveState":
			activeState = value
		}
	}
	if loadState == "" || activeState == "" {
		return "", "", errors.New("inspect transient attachment unit: incomplete state")
	}
	return loadState, activeState, nil
}

func waitForTransientUnit(ctx context.Context, unitName string, controlEnv []string) error {
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		probeCtx, cancel := context.WithTimeout(ctx, time.Second)
		loadState, activeState, err := transientUnitState(probeCtx, unitName, controlEnv)
		cancel()
		if err == nil {
			switch {
			case loadState == "loaded" && activeState == "active":
				return nil
			case loadState == "loaded" && activeState != "activating":
				return fmt.Errorf("transient attachment unit entered %s state", activeState)
			default:
				lastErr = fmt.Errorf("transient attachment unit is %s/%s", loadState, activeState)
			}
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("transient attachment unit did not start: %w", lastErr)
		case <-ticker.C:
		}
	}
}

func transientUnitStopped(loadState, activeState string) bool {
	return loadState == "not-found" || activeState == "inactive" || activeState == "failed"
}

func stopTransientUnit(unitName string, controlEnv []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	loadState, activeState, stateErr := transientUnitState(ctx, unitName, controlEnv)
	if stateErr == nil && transientUnitStopped(loadState, activeState) {
		return nil
	}
	command := exec.CommandContext(
		ctx,
		"/usr/bin/systemctl",
		"--user",
		"stop",
		"--no-ask-password",
		unitName,
	)
	command.Env = controlEnv
	output, err := command.CombinedOutput()
	if err == nil {
		return nil
	}
	loadState, activeState, afterErr := transientUnitState(ctx, unitName, controlEnv)
	if afterErr == nil && transientUnitStopped(loadState, activeState) {
		return nil
	}
	message := strings.TrimSpace(string(output))
	if len(message) > 512 {
		message = message[:512]
	}
	return fmt.Errorf("stop transient attachment unit: %w: %s", err, message)
}

type ptySession struct {
	file       *os.File
	cmd        *exec.Cmd
	unitName   string
	controlEnv []string

	closeOnce     sync.Once
	closeErr      error
	terminateOnce sync.Once
	terminateErr  error
	waitOnce      sync.Once
	waitCode      int
	waitErr       error
}

func (p *ptySession) Read(data []byte) (int, error)  { return p.file.Read(data) }
func (p *ptySession) Write(data []byte) (int, error) { return p.file.Write(data) }

func (p *ptySession) Resize(cols, rows int) error {
	if p == nil || p.file == nil {
		return errors.New("PTY is closed")
	}
	dimensions := (Dimensions{Cols: cols, Rows: rows}).normalized()
	return pty.Setsize(p.file, &pty.Winsize{
		Cols: uint16(dimensions.Cols),
		Rows: uint16(dimensions.Rows),
	})
}

func (p *ptySession) terminate() error {
	if p == nil {
		return nil
	}
	p.terminateOnce.Do(func() {
		if p.unitName != "" {
			p.terminateErr = stopTransientUnit(p.unitName, p.controlEnv)
			if p.terminateErr == nil {
				return
			}
		}
		if p.cmd != nil && p.cmd.Process != nil {
			// Direct foreground runs use a process-group fallback. The
			// supported systemd deployment owns the client through a transient
			// service instead.
			if err := syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
				p.terminateErr = errors.Join(p.terminateErr, err)
			}
		}
	})
	return p.terminateErr
}

func (p *ptySession) Close() error {
	if p == nil {
		return nil
	}
	p.closeOnce.Do(func() {
		if p.file != nil {
			p.closeErr = p.file.Close()
		}
		p.closeErr = errors.Join(p.closeErr, p.terminate())
	})
	return p.closeErr
}

func (p *ptySession) Wait() (int, error) {
	if p == nil || p.cmd == nil {
		return -1, errors.New("PTY is not started")
	}
	p.waitOnce.Do(func() {
		p.waitCode = -1
		p.waitErr = p.cmd.Wait()
		var exitErr *exec.ExitError
		if errors.As(p.waitErr, &exitErr) {
			// The operating-system status is returned separately. A non-zero
			// client exit is not a teardown failure.
			p.waitErr = nil
		}
		if p.cmd.ProcessState != nil {
			p.waitCode = p.cmd.ProcessState.ExitCode()
		}
		// Terminate before any bridge-side write timeout. The supported
		// deployment stops a transient systemd service with KillMode set to
		// control-group, covering descendants that changed session or process
		// group while keeping cgroup controls read-only inside the child.
		p.waitErr = errors.Join(p.waitErr, p.terminate())
	})
	return p.waitCode, p.waitErr
}

type outputQueue struct {
	mu     sync.Mutex
	items  [][]byte
	bytes  int
	limit  int
	closed bool
	wake   chan struct{}
}

func newOutputQueue(limit int) *outputQueue {
	if limit <= 0 {
		limit = 1 * 1024 * 1024
	}
	return &outputQueue{limit: limit, wake: make(chan struct{})}
}

func (q *outputQueue) push(data []byte) bool {
	if len(data) == 0 {
		return true
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed || len(data) > q.limit || q.bytes+len(data) > q.limit {
		return false
	}
	copyOfData := append([]byte(nil), data...)
	q.items = append(q.items, copyOfData)
	q.bytes += len(copyOfData)
	close(q.wake)
	q.wake = make(chan struct{})
	return true
}

func (q *outputQueue) pop() ([]byte, bool) {
	for {
		q.mu.Lock()
		if len(q.items) > 0 {
			item := q.items[0]
			q.items[0] = nil
			q.items = q.items[1:]
			q.bytes -= len(item)
			q.mu.Unlock()
			return item, true
		}
		if q.closed {
			q.mu.Unlock()
			return nil, false
		}
		wake := q.wake
		q.mu.Unlock()
		<-wake
	}
}

func (q *outputQueue) close() {
	q.mu.Lock()
	if !q.closed {
		q.closed = true
		close(q.wake)
	}
	q.mu.Unlock()
}

type socketWriter struct {
	mu       sync.Mutex
	conn     *websocket.Conn
	deadline time.Duration
}

func (w *socketWriter) message(messageType int, payload []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.conn.SetWriteDeadline(time.Now().Add(w.deadline)); err != nil {
		return err
	}
	return w.conn.WriteMessage(messageType, payload)
}

func (w *socketWriter) control(messageType int, payload []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	deadline := time.Now().Add(w.deadline)
	return w.conn.WriteControl(messageType, payload, deadline)
}

type bridgeEventKind uint8

const (
	bridgePTYOverflow bridgeEventKind = iota
	bridgeInputClosed
	bridgeProtocolError
	bridgeOutputError
	bridgePingError
	bridgeCompletionError
)

type bridgeEvent struct {
	kind bridgeEventKind
	err  error
	text string
}

type bridgeWaitResult struct {
	code int
	err  error
}

// runBridge owns all goroutines and resources associated with one upgraded
// connection. It never touches any other attachment or the server listener.
func runBridge(ctx context.Context, conn *websocket.Conn, session PTYSession, completions AgentCompletionSource, cfg Config) error {
	cfg = cfg.withDefaults()
	writer := &socketWriter{conn: conn, deadline: cfg.WriteTimeout}
	if err := writer.message(websocket.TextMessage, encodeReady()); err != nil {
		closeErr := session.Close()
		_, waitErr := session.Wait()
		_ = conn.Close()
		return errors.Join(closeErr, waitErr)
	}

	bridgeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	queue := newOutputQueue(cfg.OutputQueueBytes)
	events := make(chan bridgeEvent, 8)
	waitResults := make(chan bridgeWaitResult, 1)
	readerDone := make(chan struct{})
	writerDone := make(chan struct{})

	_ = conn.SetReadDeadline(time.Now().Add(cfg.PongTimeout))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(cfg.PongTimeout))
	})

	go func() {
		defer close(readerDone)
		defer queue.close()
		buffer := make([]byte, 32*1024)
		for {
			count, err := session.Read(buffer)
			if count > 0 && !queue.push(buffer[:count]) {
				events <- bridgeEvent{kind: bridgePTYOverflow, err: errors.New("PTY output queue is full")}
				return
			}
			if err != nil {
				return
			}
		}
	}()

	go func() {
		defer close(writerDone)
		for {
			payload, ok := queue.pop()
			if !ok {
				return
			}
			if err := writer.message(websocket.BinaryMessage, payload); err != nil {
				events <- bridgeEvent{kind: bridgeOutputError, err: err}
				return
			}
		}
	}()

	go func() {
		for {
			messageType, payload, err := conn.ReadMessage()
			if err != nil {
				events <- bridgeEvent{kind: bridgeInputClosed, err: err}
				return
			}
			switch messageType {
			case websocket.BinaryMessage:
				if _, err := writeAll(session, payload); err != nil {
					events <- bridgeEvent{kind: bridgeInputClosed, err: err}
					return
				}
			case websocket.TextMessage:
				dimensions, err := decodeResize(payload)
				if err != nil {
					events <- bridgeEvent{kind: bridgeProtocolError, err: err, text: "invalid resize message"}
					return
				}
				if err := session.Resize(dimensions.Cols, dimensions.Rows); err != nil {
					events <- bridgeEvent{kind: bridgeProtocolError, err: err, text: "resize failed"}
					return
				}
			default:
				events <- bridgeEvent{kind: bridgeProtocolError, err: errTextAfterHello, text: "unexpected WebSocket message"}
				return
			}
		}
	}()

	go func() {
		ticker := time.NewTicker(cfg.PingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := writer.control(websocket.PingMessage, nil); err != nil {
					events <- bridgeEvent{kind: bridgePingError, err: err}
					return
				}
			case <-bridgeCtx.Done():
				return
			}
		}
	}()

	go forwardAgentCompletions(bridgeCtx, completions, func(completion AgentCompletion) error {
		err := writer.message(websocket.TextMessage, encodeAgentDone(completion))
		if err != nil {
			select {
			case events <- bridgeEvent{kind: bridgeCompletionError, err: err}:
			case <-bridgeCtx.Done():
			}
		}
		return err
	})

	go func() {
		code, err := session.Wait()
		waitResults <- bridgeWaitResult{code: code, err: err}
	}()

	var result bridgeWaitResult
	resultReceived := false
	var terminalMessage string
	select {
	case result = <-waitResults:
		resultReceived = true
	case event := <-events:
		switch event.kind {
		case bridgePTYOverflow:
			terminalMessage = "terminal output is too busy"
		case bridgeProtocolError:
			terminalMessage = event.text
		case bridgeInputClosed, bridgeOutputError, bridgePingError, bridgeCompletionError:
			// The peer or transport is already gone; only cleanup is needed.
		}
	case <-ctx.Done():
	}

	contextError := ctx.Err()
	if errors.Is(contextError, context.DeadlineExceeded) {
		terminalMessage = "session expired"
	}
	naturalExit := resultReceived && contextError == nil
	cancel()

	var cleanupErr error
	if naturalExit {
		select {
		case <-readerDone:
		case <-time.After(cfg.WriteTimeout):
			cleanupErr = errors.Join(cleanupErr, session.Close())
		}
		queue.close()
		select {
		case <-writerDone:
		case <-time.After(cfg.WriteTimeout):
		}
		cleanupErr = errors.Join(cleanupErr, session.Close())
	} else {
		cleanupErr = errors.Join(cleanupErr, session.Close())
		queue.close()
		if !resultReceived {
			select {
			case result = <-waitResults:
				resultReceived = true
			case <-time.After(cfg.WriteTimeout):
				cleanupErr = errors.Join(cleanupErr, errors.New("PTY process did not finish after teardown"))
			}
		}
		select {
		case <-writerDone:
		case <-time.After(cfg.WriteTimeout):
		}
	}
	if resultReceived {
		cleanupErr = errors.Join(cleanupErr, result.err)
	}

	closeCode := websocket.CloseNormalClosure
	switch {
	case terminalMessage != "":
		_ = writer.message(websocket.TextMessage, encodeError(terminalMessage))
		closeCode = websocket.ClosePolicyViolation
	case errors.Is(contextError, context.Canceled):
		closeCode = websocket.CloseServiceRestart
	case contextError == nil && resultReceived:
		_ = writer.message(websocket.TextMessage, encodeExit(result.code))
	}
	_ = writer.control(websocket.CloseMessage, websocket.FormatCloseMessage(closeCode, ""))
	_ = conn.Close()
	return cleanupErr
}

func writeAll(writer io.Writer, payload []byte) (int, error) {
	total := 0
	for total < len(payload) {
		count, err := writer.Write(payload[total:])
		total += count
		if err != nil {
			return total, err
		}
		if count == 0 {
			return total, io.ErrShortWrite
		}
	}
	return total, nil
}

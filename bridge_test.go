package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

func useDirectPTYMode(t *testing.T) {
	t.Helper()
	t.Setenv("INVOCATION_ID", "")
}

func TestBuildClientEnvExcludesHerdrEnvAndUnknownValues(t *testing.T) {
	env := buildClientEnv([]string{
		"HERDR_ENV=production",
		"HOME=/tmp/not-home",
		"TERM=xterm-256color",
		"SECRET=value",
	})
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "HERDR_ENV=") || strings.Contains(joined, "SECRET=") {
		t.Fatalf("filtered environment leaked a value: %q", joined)
	}
	if !strings.Contains(joined, "HOME=/tmp/not-home") {
		t.Fatalf("allowlisted HOME was not retained: %q", joined)
	}
}

func TestSystemdControlEnvironmentExcludesApplicationValues(t *testing.T) {
	env := buildSystemdControlEnv([]string{
		"HERDR_WEB_CLIENT_OIDC_AUDIENCE=secret",
		"HOME=/home/test",
		"DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus",
		"XDG_RUNTIME_DIR=/run/user/1000",
		"SECRET=value",
	})
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "HERDR_WEB_CLIENT_") || strings.Contains(joined, "SECRET=") {
		t.Fatalf("systemd control environment leaked an application value: %q", joined)
	}
	if !strings.Contains(joined, "DBUS_SESSION_BUS_ADDRESS=") || !strings.Contains(joined, "XDG_RUNTIME_DIR=") {
		t.Fatalf("systemd control environment omitted the user manager address: %q", joined)
	}
}

func TestTransientUnitArgumentsPreservePTYAndCleanupBoundaries(t *testing.T) {
	arguments := transientUnitArguments(
		"herdr-web-client-attachment-test.service",
		"herdr-web-client.service",
		"/opt/herdr-web-client",
		"/opt/herdr",
		"/srv/work",
		"/home/test/.config/herdr",
		"/run/user/1000",
		[]string{"/dev/dri", "/dev/video0"},
		[]string{"HOME=/home/test", "TERM=xterm-256color"},
	)
	joined := strings.Join(arguments, "\n")
	properties := make([]string, 0)
	for _, argument := range arguments {
		if property, found := strings.CutPrefix(argument, "--property="); found {
			properties = append(properties, property)
		}
	}
	expectedProperties := []string{
		"BindsTo=herdr-web-client.service",
		"After=herdr-web-client.service",
		"NoNewPrivileges=yes",
		"PrivateTmp=yes",
		"PrivateMounts=yes",
		"ProtectSystem=strict",
		"ProtectHome=read-only",
		`ReadWritePaths="/home/test/.config/herdr"`,
		`InaccessiblePaths="-/run/user/1000/bus" "-/run/user/1000/systemd/private" "-/dev/dri" "-/dev/video0"`,
		"ProtectKernelTunables=yes",
		"ProtectControlGroups=yes",
		"RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6",
		"RestrictNamespaces=yes",
		"RestrictRealtime=yes",
		"RestrictSUIDSGID=yes",
		"LockPersonality=yes",
		"SystemCallArchitectures=native",
		"KillMode=control-group",
		"KillSignal=SIGKILL",
		"FinalKillSignal=SIGKILL",
		"SendSIGKILL=yes",
		"TimeoutStopSec=1s",
		"TasksMax=64",
		"MemoryMax=512M",
		"CPUQuota=200%",
		"LimitNOFILE=4096",
		"UMask=0077",
	}
	if !slices.Equal(properties, expectedProperties) {
		t.Fatalf("transient unit properties = %q, want %q", properties, expectedProperties)
	}
	for _, required := range []string{
		"--service-type=exec",
		"--expand-environment=no",
		"--pty",
		"--property=BindsTo=herdr-web-client.service",
		"--property=After=herdr-web-client.service",
		"--property=ProtectControlGroups=yes",
		`--property=InaccessiblePaths="-/run/user/1000/bus" "-/run/user/1000/systemd/private" "-/dev/dri" "-/dev/video0"`,
		"--property=RestrictNamespaces=yes",
		"--property=KillMode=control-group",
		"--property=KillSignal=SIGKILL",
		"--property=MemoryMax=512M",
		"--property=CPUQuota=200%",
		"--property=LimitNOFILE=4096",
		"--property=UMask=0077",
		attachmentExecArgument,
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("transient unit arguments omitted %q: %q", required, arguments)
		}
	}
	if got, want := strings.Join(arguments[len(arguments)-6:], "|"), "--|/opt/herdr-web-client|"+attachmentExecArgument+"|/opt/herdr|HOME=/home/test|TERM=xterm-256color"; got != want {
		t.Fatalf("transient application arguments = %q, want %q", got, want)
	}
}

func TestQuoteSystemdPathProtectsWhitespaceSpecifiersAndControls(t *testing.T) {
	got := quoteSystemdPath("/home/Herdr User/%n/line\n\"quoted\"")
	want := `"/home/Herdr User/%%n/line\x0a\"quoted\""`
	if got != want {
		t.Fatalf("quoted systemd path = %q, want %q", got, want)
	}
}

func TestDevicePathsToMaskPreservesOnlyRequiredPseudoDevices(t *testing.T) {
	deviceDirectory := t.TempDir()
	for _, name := range []string{"tty", "ptmx", "pts", "null", "char", "dri", "video0"} {
		if err := os.Mkdir(filepath.Join(deviceDirectory, name), 0o700); err != nil {
			t.Fatalf("create fake device entry %q: %v", name, err)
		}
	}

	got, err := devicePathsToMask(deviceDirectory)
	if err != nil {
		t.Fatalf("enumerate fake devices: %v", err)
	}
	want := []string{
		filepath.Join(deviceDirectory, "char"),
		filepath.Join(deviceDirectory, "dri"),
		filepath.Join(deviceDirectory, "video0"),
	}
	if !slices.Equal(got, want) {
		t.Fatalf("masked device paths = %q, want %q", got, want)
	}
}

func TestPTYLauncherAppliesFilteredEnvironmentAndFixedClientArgument(t *testing.T) {
	useDirectPTYMode(t)
	dir := t.TempDir()
	executable := filepath.Join(dir, "fake-herdr")
	script := "#!/bin/sh\nprintf '%s|%s|%s|%s' \"$1\" \"${HERDR_ENV-unset}\" \"${SECRET-unset}\" \"$TERM\"\n"
	if err := os.WriteFile(executable, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake Herdr client: %v", err)
	}

	launcher := &PTYLauncher{
		Path: executable,
		Dir:  dir,
		Env: []string{
			"HERDR_ENV=1",
			"SECRET=not-for-the-child",
			"TERM=test-terminal",
		},
	}
	session, err := launcher.Start(context.Background(), 80, 24)
	if err != nil {
		t.Fatalf("start fake Herdr client: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	output := make([]byte, 256)
	count, err := session.Read(output)
	if err != nil && count == 0 {
		t.Fatalf("read fake Herdr output: %v", err)
	}
	code, _ := session.Wait()
	if code != 0 {
		t.Fatalf("fake Herdr exit code = %d, want 0", code)
	}
	if got, want := string(output[:count]), "client|unset|unset|test-terminal"; got != want {
		t.Fatalf("fake Herdr environment output = %q, want %q", got, want)
	}
}

func TestOutputQueueIsBounded(t *testing.T) {
	queue := newOutputQueue(64 * 1024)
	chunk := make([]byte, 32*1024)
	if !queue.push(chunk) {
		t.Fatal("queue rejected first data chunk below its bound")
	}
	if !queue.push(chunk) {
		t.Fatal("queue rejected second data chunk below its bound")
	}
	if queue.push([]byte{1}) {
		t.Fatal("queue accepted data above its bound")
	}
	first, ok := queue.pop()
	if !ok || len(first) != len(chunk) {
		t.Fatalf("first queue item = %d bytes, ok=%v", len(first), ok)
	}
	queue.close()
	second, ok := queue.pop()
	if !ok || len(second) != len(chunk) {
		t.Fatalf("second queue item = %d bytes, ok=%v", len(second), ok)
	}
	if _, ok := queue.pop(); ok {
		t.Fatal("closed queue returned an item")
	}
}

func TestPTYLauncherRejectsCanceledContextBeforeSpawn(t *testing.T) {
	useDirectPTYMode(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	launcher := NewPTYLauncher("/does/not/exist", "/tmp")
	if _, err := launcher.Start(ctx, 80, 24); err == nil {
		t.Fatal("launcher accepted a canceled context")
	}
}

func TestPTYLauncherReportsUnavailableExecutable(t *testing.T) {
	useDirectPTYMode(t)
	launcher := NewPTYLauncher(filepath.Join(t.TempDir(), "missing-herdr"), t.TempDir())
	session, err := launcher.Start(context.Background(), 80, 24)
	if err == nil {
		t.Fatal("launcher started an unavailable Herdr executable")
	}
	if session != nil {
		t.Fatal("launcher returned a session after an executable failure")
	}
}

func TestPTYLauncherReportsUnavailableWorkdir(t *testing.T) {
	useDirectPTYMode(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}
	launcher := NewPTYLauncher(executable, filepath.Join(t.TempDir(), "missing-workdir"))
	session, err := launcher.Start(context.Background(), 80, 24)
	if err == nil {
		t.Fatal("launcher started in an unavailable working directory")
	}
	if session != nil {
		t.Fatal("launcher returned a session after a working-directory failure")
	}
}

func TestPTYSessionCloseTerminatesTheWholeProcessGroup(t *testing.T) {
	useDirectPTYMode(t)
	dir := t.TempDir()
	executable := filepath.Join(dir, "process-tree")
	script := "#!/bin/sh\nsleep 60 &\nprintf 'ready\\n'\nwait\n"
	if err := os.WriteFile(executable, []byte(script), 0o700); err != nil {
		t.Fatalf("write process-tree fixture: %v", err)
	}

	session, err := NewPTYLauncher(executable, dir).Start(context.Background(), 80, 24)
	if err != nil {
		t.Fatalf("start process-tree fixture: %v", err)
	}
	concrete, ok := session.(*ptySession)
	if !ok {
		t.Fatalf("session type = %T, want *ptySession", session)
	}
	groupID := concrete.cmd.Process.Pid
	output := make([]byte, 64)
	if _, err := session.Read(output); err != nil {
		t.Fatalf("read process-tree readiness: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("close process-tree session: %v", err)
	}
	_, _ = session.Wait()

	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		err := syscall.Kill(-groupID, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if err != nil {
			t.Fatalf("probe process group %d: %v", groupID, err)
		}
		select {
		case <-deadline.C:
			t.Fatalf("process group %d survived session close", groupID)
		case <-ticker.C:
		}
	}
}

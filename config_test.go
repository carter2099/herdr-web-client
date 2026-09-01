package main

import (
	"path/filepath"
	"testing"
)

func validTestConfig() Config {
	cfg := DefaultConfig()
	cfg.PublicOrigin = "https://web.example.test"
	return cfg
}

func TestDefaultConfigUsesRuntimeDerivedValues(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := DefaultConfig()
	if cfg.ListenAddr != "127.0.0.1:8080" {
		t.Fatalf("default listen address = %q, want 127.0.0.1:8080", cfg.ListenAddr)
	}
	for value, want := range map[string]string{
		cfg.HerdrPath:    filepath.Join(home, ".local", "bin", "herdr"),
		cfg.HerdrWorkdir: home,
		cfg.HerdrSocket:  filepath.Join(home, ".config", "herdr", "herdr.sock"),
	} {
		if value != want {
			t.Fatalf("runtime default = %q, want %q", value, want)
		}
	}
}

func TestConfigRejectsNonLoopbackListener(t *testing.T) {
	cfg := validTestConfig()
	cfg.ListenAddr = "0.0.0.0:8080"
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate accepted a wildcard listener")
	}
}

func TestConfigAcceptsNumericLoopbackListeners(t *testing.T) {
	for _, listenAddr := range []string{"127.0.0.1:8080", "[::1]:8080"} {
		t.Run(listenAddr, func(t *testing.T) {
			cfg := validTestConfig()
			cfg.ListenAddr = listenAddr
			if err := cfg.Validate(); err != nil {
				t.Fatalf("Validate(%q): %v", listenAddr, err)
			}
		})
	}
}

func TestConfigRequiresPublicOrigin(t *testing.T) {
	cfg := validTestConfig()
	cfg.PublicOrigin = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate accepted a missing public origin")
	}
}

func TestConfigRejectsMalformedPublicOrigin(t *testing.T) {
	for _, suffix := range []string{"/path", "?", "#"} {
		t.Run(suffix, func(t *testing.T) {
			cfg := validTestConfig()
			cfg.PublicOrigin += suffix
			if err := cfg.Validate(); err == nil {
				t.Fatal("Validate accepted malformed public origin")
			}
		})
	}
}

func TestConfigRequiresBrowserCanonicalPublicOrigin(t *testing.T) {
	for _, origin := range []string{
		"https://WEB.example.test",
		"https://web.example.test:443",
		"https://web.example.test:08443",
		"https://[2001:0db8::1]",
		"https://123",
		"https://web_example.test",
	} {
		t.Run(origin, func(t *testing.T) {
			cfg := validTestConfig()
			cfg.PublicOrigin = origin
			if err := cfg.Validate(); err == nil {
				t.Fatalf("Validate accepted noncanonical public origin %q", origin)
			}
		})
	}
}

func TestConfigAcceptsCanonicalPublicOrigins(t *testing.T) {
	for _, origin := range []string{
		"https://web.example.test",
		"https://web.example.test:8443",
		"https://127.0.0.1:8443",
		"https://[2001:db8::1]:8443",
	} {
		t.Run(origin, func(t *testing.T) {
			cfg := validTestConfig()
			cfg.PublicOrigin = origin
			if err := cfg.Validate(); err != nil {
				t.Fatalf("Validate(%q): %v", origin, err)
			}
		})
	}
}

func TestConfigRejectsRelativeHerdrPaths(t *testing.T) {
	for _, field := range []string{"path", "workdir", "socket"} {
		t.Run(field, func(t *testing.T) {
			cfg := validTestConfig()
			switch field {
			case "path":
				cfg.HerdrPath = "bin/herdr"
			case "workdir":
				cfg.HerdrWorkdir = "workdir"
			case "socket":
				cfg.HerdrSocket = "herdr.sock"
			}
			if err := cfg.Validate(); err == nil {
				t.Fatalf("Validate accepted relative Herdr %s", field)
			}
		})
	}
}

func TestLoadConfigUsesOnlyCanonicalEnvironment(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(listenAddrEnv, "127.0.0.1:19090")
	t.Setenv(publicOriginEnv, "https://web.custom.test")
	t.Setenv(herdrPathEnv, filepath.Join(home, "bin", "herdr"))
	t.Setenv(herdrWorkdirEnv, filepath.Join(home, "workspace"))
	t.Setenv(herdrSocketEnv, filepath.Join(home, "run", "herdr.sock"))

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.ListenAddr != "127.0.0.1:19090" || cfg.PublicOrigin != "https://web.custom.test" ||
		cfg.HerdrPath != filepath.Join(home, "bin", "herdr") ||
		cfg.HerdrWorkdir != filepath.Join(home, "workspace") ||
		cfg.HerdrSocket != filepath.Join(home, "run", "herdr.sock") {
		t.Fatalf("unexpected loaded config: %+v", cfg)
	}
}

func TestLoadConfigRejectsExplicitEmptyValues(t *testing.T) {
	t.Setenv(publicOriginEnv, "https://web.example.test")
	for _, name := range []string{listenAddrEnv, publicOriginEnv, herdrPathEnv, herdrWorkdirEnv, herdrSocketEnv} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, "")
			if _, err := LoadConfig(); err == nil {
				t.Fatalf("LoadConfig accepted explicit empty %s", name)
			}
		})
	}
}

func TestStrictHostAndOrigin(t *testing.T) {
	cfg := validTestConfig()
	if !cfg.strictHost("web.example.test") {
		t.Fatal("strictHost rejected configured host")
	}
	if !cfg.strictOrigin(cfg.PublicOrigin) {
		t.Fatal("strictOrigin rejected configured origin")
	}
	if cfg.strictOrigin(cfg.PublicOrigin + "/") {
		t.Fatal("strictOrigin accepted a non-exact origin")
	}
}

package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func validTestConfig() Config {
	cfg := DefaultConfig()
	cfg.PublicOrigin = "https://web.example.test"
	cfg.Issuer = "https://issuer.example.test"
	cfg.Audience = "audience"
	cfg.AssertionHeader = "X-Test-Assertion"
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

func TestConfigRequiresOIDCIdentitySettings(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "public origin", mutate: func(cfg *Config) { cfg.PublicOrigin = "" }},
		{name: "issuer", mutate: func(cfg *Config) { cfg.Issuer = "" }},
		{name: "audience", mutate: func(cfg *Config) { cfg.Audience = "" }},
		{name: "assertion header", mutate: func(cfg *Config) { cfg.AssertionHeader = "" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := validTestConfig()
			test.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatalf("Validate accepted missing %s", test.name)
			}
		})
	}
}

func TestConfigAcceptsIssuerTenantPathAndJWKSURL(t *testing.T) {
	cfg := validTestConfig()
	cfg.Issuer = "https://issuer.example.test/tenant/one"
	cfg.JWKSURL = "https://keys.example.test/tenant/jwks"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestConfigRejectsMalformedURLs(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "origin path", mutate: func(cfg *Config) { cfg.PublicOrigin += "/path" }},
		{name: "origin query", mutate: func(cfg *Config) { cfg.PublicOrigin += "?" }},
		{name: "origin fragment", mutate: func(cfg *Config) { cfg.PublicOrigin += "#" }},
		{name: "issuer query", mutate: func(cfg *Config) { cfg.Issuer += "?" }},
		{name: "issuer fragment", mutate: func(cfg *Config) { cfg.Issuer += "#" }},
		{name: "issuer insecure", mutate: func(cfg *Config) { cfg.Issuer = "http://issuer.example.test" }},
		{name: "JWKS insecure", mutate: func(cfg *Config) { cfg.JWKSURL = "http://keys.example.test/jwks" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := validTestConfig()
			test.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("Validate accepted malformed URL")
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

func TestConfigRejectsInvalidAssertionHeaders(t *testing.T) {
	for _, header := range []string{
		"",
		"X Bad",
		"Host",
		"origin",
		"Cookie",
		"Connection",
		"upgrade",
		"Content-Length",
		"Transfer-Encoding",
		"Trailer",
		"TE",
		"Keep-Alive",
		"Proxy-Connection",
		"Sec-WebSocket-Key",
		"sec-websocket-version",
		"Sec-WebSocket-Protocol",
		"Sec-WebSocket-Extensions",
		sessionRequestHeader,
	} {
		t.Run(strings.ReplaceAll(header, " ", "_"), func(t *testing.T) {
			cfg := validTestConfig()
			cfg.AssertionHeader = header
			if err := cfg.Validate(); err == nil {
				t.Fatalf("Validate accepted assertion header %q", header)
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
	t.Setenv(issuerEnv, "https://issuer.custom.test/tenant")
	t.Setenv(audienceEnv, "custom-audience")
	t.Setenv(assertionHeaderEnv, "X-Custom-Assertion")
	t.Setenv(jwksURLEnv, "https://keys.custom.test/jwks")
	t.Setenv(herdrPathEnv, filepath.Join(home, "bin", "herdr"))
	t.Setenv(herdrWorkdirEnv, filepath.Join(home, "workspace"))
	t.Setenv(herdrSocketEnv, filepath.Join(home, "run", "herdr.sock"))

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.ListenAddr != "127.0.0.1:19090" || cfg.PublicOrigin != "https://web.custom.test" ||
		cfg.Issuer != "https://issuer.custom.test/tenant" || cfg.Audience != "custom-audience" ||
		cfg.AssertionHeader != "X-Custom-Assertion" || cfg.JWKSURL != "https://keys.custom.test/jwks" {
		t.Fatalf("unexpected loaded identity config: %+v", cfg)
	}
}

func TestLoadConfigRejectsExplicitEmptyValues(t *testing.T) {
	required := map[string]string{
		publicOriginEnv:    "https://web.example.test",
		issuerEnv:          "https://issuer.example.test",
		audienceEnv:        "audience",
		assertionHeaderEnv: "X-Test-Assertion",
	}
	for name, value := range required {
		t.Setenv(name, value)
	}
	for _, name := range []string{listenAddrEnv, publicOriginEnv, issuerEnv, audienceEnv, assertionHeaderEnv, jwksURLEnv, herdrPathEnv, herdrWorkdirEnv, herdrSocketEnv} {
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

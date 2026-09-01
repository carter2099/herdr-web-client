package main

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	defaultListenAddr = "127.0.0.1:8080"

	listenAddrEnv   = "HERDR_WEB_CLIENT_LISTEN_ADDR"
	publicOriginEnv = "HERDR_WEB_CLIENT_PUBLIC_ORIGIN"
	herdrPathEnv    = "HERDR_WEB_CLIENT_HERDR_PATH"
	herdrWorkdirEnv = "HERDR_WEB_CLIENT_HERDR_WORKDIR"
	herdrSocketEnv  = "HERDR_WEB_CLIENT_HERDR_SOCKET"
)

// Config contains the security and runtime policy for the web attachment
// server and the Herdr process it launches.
type Config struct {
	ListenAddr   string
	PublicOrigin string
	HerdrPath    string
	HerdrWorkdir string
	HerdrSocket  string

	NonceTTL         time.Duration
	MaxInboundBytes  int64
	OutputQueueBytes int
	HelloTimeout     time.Duration
	PingInterval     time.Duration
	PongTimeout      time.Duration
	WriteTimeout     time.Duration
}

// DefaultConfig returns safe defaults for optional runtime settings. The
// public origin is a deployment requirement and intentionally has no default.
func DefaultConfig() Config {
	cfg := Config{
		ListenAddr:       defaultListenAddr,
		NonceTTL:         60 * time.Second,
		MaxInboundBytes:  64 * 1024,
		OutputQueueBytes: 1 * 1024 * 1024,
		HelloTimeout:     10 * time.Second,
		PingInterval:     30 * time.Second,
		PongTimeout:      60 * time.Second,
		WriteTimeout:     10 * time.Second,
	}
	if home := runtimeHomeDir(); home != "" {
		cfg.HerdrPath = filepath.Join(home, ".local", "bin", "herdr")
		cfg.HerdrWorkdir = home
		cfg.HerdrSocket = filepath.Join(home, ".config", "herdr", "herdr.sock")
	}
	return cfg
}

func runtimeHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return ""
	}
	return filepath.Clean(home)
}

// LoadConfig reads deployment settings and validates the complete runtime
// configuration.
func LoadConfig() (Config, error) {
	cfg := DefaultConfig()
	for _, setting := range []struct {
		name string
		dst  *string
	}{
		{listenAddrEnv, &cfg.ListenAddr},
		{publicOriginEnv, &cfg.PublicOrigin},
		{herdrPathEnv, &cfg.HerdrPath},
		{herdrWorkdirEnv, &cfg.HerdrWorkdir},
		{herdrSocketEnv, &cfg.HerdrSocket},
	} {
		value, ok := os.LookupEnv(setting.name)
		if !ok {
			continue
		}
		if value == "" {
			return Config{}, fmt.Errorf("%s must not be empty", setting.name)
		}
		*setting.dst = value
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate checks all externally relevant configuration, including the
// loopback-only bind policy and exact request-check origins.
func (c Config) Validate() error {
	c = c.withDefaults()
	return c.validateServer()
}

// validateServer is used by tests and by NewServer.
func (c Config) validateServer() error {
	c = c.withDefaults()
	if strings.TrimSpace(c.ListenAddr) == "" {
		return errors.New("listen address is required")
	}
	host, portText, err := net.SplitHostPort(c.ListenAddr)
	if err != nil {
		return fmt.Errorf("invalid listen address %q: %w", c.ListenAddr, err)
	}
	if host == "" {
		return errors.New("listen address must name a loopback address")
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() || ip.IsUnspecified() {
		return fmt.Errorf("listen address %q is not loopback-only", c.ListenAddr)
	}
	if portText == "" {
		return errors.New("listen port is required")
	}
	for _, char := range portText {
		if char < '0' || char > '9' {
			return fmt.Errorf("invalid listen port %q", portText)
		}
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("invalid listen port %q", portText)
	}

	if err := validatePublicOrigin(c.PublicOrigin); err != nil {
		return err
	}
	for _, setting := range []struct {
		name  string
		value string
	}{
		{"Herdr path", c.HerdrPath},
		{"Herdr workdir", c.HerdrWorkdir},
		{"Herdr socket", c.HerdrSocket},
	} {
		if err := validateAbsolutePath(setting.name, setting.value); err != nil {
			return err
		}
	}
	if c.NonceTTL <= 0 {
		return errors.New("nonce TTL must be positive")
	}
	if c.MaxInboundBytes <= 0 || c.MaxInboundBytes > 64*1024 {
		return errors.New("inbound message limit must be between 1 and 65536 bytes")
	}
	if c.OutputQueueBytes < 64*1024 || c.OutputQueueBytes > 16*1024*1024 {
		return errors.New("output queue limit must be between 65536 and 16777216 bytes")
	}
	if c.HelloTimeout <= 0 || c.PingInterval <= 0 || c.PongTimeout <= 0 || c.WriteTimeout <= 0 {
		return errors.New("websocket timeouts must be positive")
	}
	return nil
}

func validatePublicOrigin(origin string) error {
	if strings.TrimSpace(origin) == "" {
		return errors.New("public origin is required")
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Hostname() == "" ||
		parsed.User != nil || parsed.Opaque != "" || parsed.Path != "" || parsed.RawPath != "" ||
		parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || strings.Contains(origin, "#") ||
		!validURLPort(parsed) {
		return fmt.Errorf("public origin must be an exact https origin without a path, query, or fragment: %q", origin)
	}
	if strings.TrimSpace(origin) != origin {
		return errors.New("public origin must not have surrounding whitespace")
	}
	canonical, err := canonicalPublicOrigin(parsed)
	if err != nil {
		return fmt.Errorf("public origin is not browser-canonical: %w", err)
	}
	if origin != canonical {
		return fmt.Errorf("public origin must use browser-canonical form %q", canonical)
	}
	return nil
}

func canonicalPublicOrigin(origin *url.URL) (string, error) {
	hostname := origin.Hostname()
	canonicalHost := ""
	isIPv6 := false
	if address, err := netip.ParseAddr(hostname); err == nil {
		if address.Zone() != "" {
			return "", errors.New("scoped IP addresses are not allowed")
		}
		canonicalHost = address.String()
		isIPv6 = address.Is6()
	} else {
		canonicalHost = strings.ToLower(hostname)
		if !validASCIIDNSName(canonicalHost) {
			return "", fmt.Errorf("invalid DNS name %q", hostname)
		}
	}

	port := origin.Port()
	if port == "443" {
		return "", errors.New("default HTTPS port must be omitted")
	}
	if port != "" {
		value, _ := strconv.Atoi(port)
		if strconv.Itoa(value) != port {
			return "", errors.New("port must not contain leading zeroes")
		}
		return "https://" + net.JoinHostPort(canonicalHost, port), nil
	}
	if isIPv6 {
		canonicalHost = "[" + canonicalHost + "]"
	}
	return "https://" + canonicalHost, nil
}

func validASCIIDNSName(hostname string) bool {
	if hostname == "" || len(hostname) > 253 || strings.HasSuffix(hostname, ".") {
		return false
	}
	numeric := true
	for _, label := range strings.Split(hostname, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for index := range label {
			char := label[index]
			if char >= '0' && char <= '9' {
				continue
			}
			numeric = false
			if (char >= 'a' && char <= 'z') || char == '-' {
				continue
			}
			return false
		}
	}
	return !numeric
}

func validURLPort(parsed *url.URL) bool {
	if parsed == nil {
		return false
	}
	port := parsed.Port()
	if port == "" {
		return !strings.HasSuffix(parsed.Host, ":")
	}
	for _, char := range port {
		if char < '0' || char > '9' {
			return false
		}
	}
	value, err := strconv.Atoi(port)
	return err == nil && value >= 1 && value <= 65535
}

func validateAbsolutePath(name, value string) error {
	if value == "" || !filepath.IsAbs(value) || strings.IndexByte(value, 0) >= 0 {
		return fmt.Errorf("%s must be an absolute path: %q", name, value)
	}
	return nil
}

func (c Config) withDefaults() Config {
	defaults := DefaultConfig()
	if c.ListenAddr == "" {
		c.ListenAddr = defaults.ListenAddr
	}
	if c.HerdrPath == "" {
		c.HerdrPath = defaults.HerdrPath
	}
	if c.HerdrWorkdir == "" {
		c.HerdrWorkdir = defaults.HerdrWorkdir
	}
	if c.HerdrSocket == "" {
		c.HerdrSocket = defaults.HerdrSocket
	}
	if c.NonceTTL == 0 {
		c.NonceTTL = defaults.NonceTTL
	}
	if c.MaxInboundBytes == 0 {
		c.MaxInboundBytes = defaults.MaxInboundBytes
	}
	if c.OutputQueueBytes == 0 {
		c.OutputQueueBytes = defaults.OutputQueueBytes
	}
	if c.HelloTimeout == 0 {
		c.HelloTimeout = defaults.HelloTimeout
	}
	if c.PingInterval == 0 {
		c.PingInterval = defaults.PingInterval
	}
	if c.PongTimeout == 0 {
		c.PongTimeout = defaults.PongTimeout
	}
	if c.WriteTimeout == 0 {
		c.WriteTimeout = defaults.WriteTimeout
	}
	return c
}

func (c Config) publicHost() string {
	origin, err := url.Parse(c.PublicOrigin)
	if err != nil {
		return ""
	}
	return origin.Host
}

func (c Config) strictHost(host string) bool {
	return host != "" && host == c.publicHost()
}

func (c Config) strictOrigin(origin string) bool {
	return origin != "" && origin == c.PublicOrigin
}

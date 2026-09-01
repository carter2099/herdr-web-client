# Herdr Web

Herdr Web is a self-hosted browser terminal for a running Herdr installation. One Linux executable serves the embedded web client, validates an OIDC assertion injected by your reverse proxy, and bridges one authenticated browser session to `herdr client` in a same-user PTY.

The design is fail-closed: a loopback-only listener, exact `Host`/`Origin` checks, a single-use subject-bound session nonce, one active attachment per process, bounded queues and deadlines, and direct `herdr client` execution with a filtered child environment. There is no built-in login page and no container image; the reverse proxy is the public TLS and authentication boundary.

## Requirements

- Linux `amd64` or `arm64`.
- Per-user systemd 254 or newer (unit supplied below). Never run it as a system service or as root.
- An `herdr` executable and Herdr Unix socket on the same account.
- An HTTPS reverse proxy that enforces the OIDC contract below.
- Chromium is the only browser with a compatibility guarantee.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/carter2099/herdr-web-client/main/install.sh | bash
```

To install a specific release:

```sh
curl -fsSL https://raw.githubusercontent.com/carter2099/herdr-web-client/main/install.sh | bash -s -- v1.2.3
```

The script verifies the archive against the published `SHA256SUMS` and installs `~/.local/bin/herdr-web-client` and `~/.config/systemd/user/herdr-web-client.service`. It prepares an empty `~/.config/herdr-web-client/env` on a first install and never touches existing configuration. Reinstalling (same command, any version) replaces the binary and unit; finish an update with `systemctl --user daemon-reload && systemctl --user restart herdr-web-client.service`.

From source (Bun exactly 1.3.14 and Go 1.27 required):

```sh
git clone https://github.com/carter2099/herdr-web-client.git
cd herdr-web-client
bun install --frozen-lockfile
scripts/build "$HOME/.local/bin/herdr-web-client"
```

## Configure

The service reads `~/.config/herdr-web-client/env` (dotenv syntax; systemd does not expand `$HOME` in it). The four authentication values have no defaults, and an explicitly empty value is an error. Keep the file mode `0600`.

| Variable | Default | Meaning |
| --- | --- | --- |
| `HERDR_WEB_CLIENT_LISTEN_ADDR` | `127.0.0.1:8080` | Numeric loopback `host:port` only. |
| `HERDR_WEB_CLIENT_PUBLIC_ORIGIN` | required | Exact browser-canonical HTTPS origin: no path, query, fragment, or user info, and omit the default `:443`. Its host must equal the forwarded `Host`. |
| `HERDR_WEB_CLIENT_OIDC_ISSUER` | required | Absolute HTTPS issuer URL, checked against the JWT `iss` claim. |
| `HERDR_WEB_CLIENT_OIDC_AUDIENCE` | required | Exact audience, checked against the JWT `aud` claim. |
| `HERDR_WEB_CLIENT_OIDC_ASSERTION_HEADER` | required | Name of the header your proxy injects the JWT into. Must be a legal field name; `Host`, `Origin`, `Cookie`, and the HTTP framing and WebSocket handshake headers are reserved and rejected. |
| `HERDR_WEB_CLIENT_OIDC_JWKS_URL` | OIDC discovery | Absolute HTTPS JWKS URL; by default the key set is discovered from the issuer. |
| `HERDR_WEB_CLIENT_HERDR_PATH` | `~/.local/bin/herdr` | Absolute path to the Herdr executable; always run with the fixed `client` argument, never through a shell. |
| `HERDR_WEB_CLIENT_HERDR_WORKDIR` | `~` | Absolute working directory for the PTY child. |
| `HERDR_WEB_CLIENT_HERDR_SOCKET` | `~/.config/herdr/herdr.sock` | Absolute path of the Herdr Unix socket for completion events. |

The Herdr path defaults are derived at runtime from the service account, so leave them unset unless you need explicit absolute paths.

A minimal environment file:

```dotenv
HERDR_WEB_CLIENT_PUBLIC_ORIGIN=https://terminal.example
HERDR_WEB_CLIENT_OIDC_ISSUER=https://identity.example/tenant
HERDR_WEB_CLIENT_OIDC_AUDIENCE=herdr-web-client
HERDR_WEB_CLIENT_OIDC_ASSERTION_HEADER=Proxy-Auth-Assertion
```

## Reverse proxy

The app expects requests that are already authenticated. Configure the proxy to:

1. terminate public TLS and forward only to the loopback listener;
2. authenticate the user with an OIDC provider and obtain a signed RS256 JWT whose issuer and audience match the configuration;
3. remove every client-supplied copy of the assertion header, then inject exactly one header with exactly one JWT value;
4. preserve the public `Host` exactly, and for WebSocket upgrades the exact browser `Origin` and exactly one `Sec-WebSocket-Protocol: herdr-web-client.v1`;
5. forward the `X-Herdr-Web-Client-Request: session` marker on `GET /api/session` (the marker itself is not authentication).

The server verifies the JWT's issuer, audience, expiry, and signature on every request. For Cloudflare Access, the assertion header is `Cf-Access-Jwt-Assertion`; issuer and audience remain deployment-specific.

## Run

```sh
systemctl --user daemon-reload
systemctl --user enable --now herdr-web-client.service
systemctl --user status herdr-web-client.service
```

Run it as the account that owns the Herdr executable, PTY, and socket — the same-user relationship is by design. `loginctl enable-linger "$USER"` keeps it running without an interactive login session.

## Troubleshooting

- **The service exits immediately.** Read `journalctl --user -u herdr-web-client.service -e`. Startup fails closed on a missing or empty required value, a non-HTTPS URL, a non-loopback listener, a reserved assertion-header name, or a non-absolute path.
- **401.** The proxy did not inject exactly one valid JWT (expired, wrong issuer or audience, or a failed signature check).
- **403.** The WebSocket `Origin` or the `/api/session` marker is missing or does not match exactly.
- **404.** The forwarded `Host` differs from `HERDR_WEB_CLIENT_PUBLIC_ORIGIN`.
- **The WebSocket does not attach.** The proxy must support upgrades and forward exactly one `herdr-web-client.v1` value. Nonces are single-use, and only one attachment can be active at a time.
- **The terminal is silent or exits.** Check that `HERDR_WEB_CLIENT_HERDR_PATH` is executable, the workdir exists, and the socket at `HERDR_WEB_CLIENT_HERDR_SOCKET` is reachable as the service user. The web client does not launch a shell or repair a broken Herdr installation.

## Development

Bun exactly 1.3.14, Go 1.27, and ShellCheck exactly 0.10.0. `web/dist` is tracked generated output — rebuild it with `scripts/build` and commit it together with source changes.

```sh
bun install --frozen-lockfile
scripts/verify        # fast checks; run before every push
scripts/verify-full   # ShellCheck, staticcheck, race tests, release packaging, Chromium E2E
```

Releases are cut by maintainers with `scripts/verify-full && scripts/release vX.Y.Z`, which requires a green `main` CI run for the exact commit. See [CONTRIBUTING.md](CONTRIBUTING.md) and [SECURITY.md](SECURITY.md).

## License

MIT — see [LICENSE](LICENSE) and [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md). Release archives carry the exact licenses for every linked module and the Go runtime under `LICENSES/`.

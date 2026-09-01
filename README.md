# Herdr Web

Herdr Web is a self-hosted browser terminal for a running Herdr installation. One Linux executable serves the embedded web client and bridges one browser session to `herdr client` in a same-user PTY.

Herdr Web does not provide authentication or user authorization. Anyone who can reach it can open a terminal as the service account. You are responsible for securing access with whatever controls fit your deployment.

The runtime limits its own surface with a loopback-only listener, exact `Host`/`Origin` checks, a single-use session nonce, one active attachment per process, bounded queues and deadlines, and direct `herdr client` execution with a filtered child environment. There is no container image.

## Requirements

- Linux `amd64` or `arm64`.
- Per-user systemd 254 or newer (unit supplied below). Never run it as a system service or as root.
- An `herdr` executable and Herdr Unix socket on the same account.
- A browser-reachable HTTPS origin for the loopback listener.
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

The service reads `~/.config/herdr-web-client/env` (dotenv syntax; systemd does not expand `$HOME` in it). The public origin has no default, and an explicitly empty value is an error. Keep the file mode `0600`.

| Variable | Default | Meaning |
| --- | --- | --- |
| `HERDR_WEB_CLIENT_LISTEN_ADDR` | `127.0.0.1:8080` | Numeric loopback `host:port` only. |
| `HERDR_WEB_CLIENT_PUBLIC_ORIGIN` | required | Exact browser-canonical HTTPS origin: no path, query, fragment, or user info, and omit the default `:443`. Its host must equal the forwarded `Host`. |
| `HERDR_WEB_CLIENT_HERDR_PATH` | `~/.local/bin/herdr` | Absolute path to the Herdr executable; always run with the fixed `client` argument, never through a shell. |
| `HERDR_WEB_CLIENT_HERDR_WORKDIR` | `~` | Absolute working directory for the PTY child. |
| `HERDR_WEB_CLIENT_HERDR_SOCKET` | `~/.config/herdr/herdr.sock` | Absolute path of the Herdr Unix socket for completion events. |

The Herdr path defaults are derived at runtime from the service account, so leave them unset unless you need explicit absolute paths.

A minimal environment file:

```dotenv
HERDR_WEB_CLIENT_PUBLIC_ORIGIN=https://terminal.example
```

## Secure access

Herdr Web intentionally has no authentication mechanism. Do not expose it to users you do not trust. Protect it using the approach appropriate for your environment, such as a private network, VPN, SSH tunnel, firewall policy, or an access-controlled gateway. The project does not require a particular authentication provider or reverse proxy.

Whatever carries traffic between the browser and the loopback listener must preserve the configured public `Host`. WebSocket upgrades must also preserve the exact browser `Origin` and `Sec-WebSocket-Protocol: herdr-web-client.v1`. The browser sends `X-Herdr-Web-Client-Request: session` on `GET /api/session`; this marker is a protocol check, not authentication.

## Run

```sh
systemctl --user daemon-reload
systemctl --user enable --now herdr-web-client.service
systemctl --user status herdr-web-client.service
```

Run it as the account that owns the Herdr executable, PTY, and socket — the same-user relationship is by design. `loginctl enable-linger "$USER"` keeps it running without an interactive login session.

## Troubleshooting

- **The service exits immediately.** Read `journalctl --user -u herdr-web-client.service -e`. Startup fails closed on a missing required value, a non-HTTPS public origin, a non-loopback listener, or a non-absolute path.
- **403.** The WebSocket `Origin` or the `/api/session` marker is missing or does not match exactly.
- **404.** The request `Host` differs from `HERDR_WEB_CLIENT_PUBLIC_ORIGIN`.
- **The WebSocket does not attach.** The network path must support upgrades and preserve exactly one `herdr-web-client.v1` value. Nonces are single-use, and only one attachment can be active at a time.
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

# Herdr

Herdr is a self-hosted browser terminal attachment for a running Herdr installation. It serves the browser client from one Linux executable, authenticates requests at an OIDC-aware reverse proxy, and bridges one authenticated browser session to `herdr client` in a same-user PTY.

The browser product and document title are **Herdr**. The repository, executable, service, and environment namespace use the `herdr-web-client` name.

## What it provides

- An authenticated terminal UI in a browser, with the production JavaScript and assets embedded in the Go executable.
- A provider-neutral OIDC assertion contract. Any reverse proxy that can inject one validated JWT header can be used; Cloudflare Access is only one example.
- Exact public `Host` and `Origin` checks, a one-time subject-bound session nonce, and a single active WebSocket attachment.
- A fixed `herdr client` child command in a PTY, with bounded message and output queues, deadlines, and filtered child environment.
- Completion notifications from the same user's Herdr Unix socket.
- A user-level systemd unit for a host deployment. There is no Docker image or container support claim.

This project is intentionally a host integration rather than a general-purpose terminal gateway. Read the trust boundaries and limitations before exposing it through a proxy.

## Architecture and trust boundaries

```text
browser
  │ HTTPS / WebSocket
  ▼
OIDC-aware reverse proxy ── validates the user and injects one signed JWT header
  │ loopback HTTP; preserves the configured Host, Origin, and WebSocket protocol
  ▼
herdr-web-client (Linux Go binary; embedded web/dist)
  │ same Unix user; fixed argv: herdr client
  ├── PTY ── herdr client
  └── Unix socket ── Herdr completion events
```

The reverse proxy is the public TLS and authentication boundary. It MUST remove any client-supplied assertion header and add exactly one header containing the signed JWT. The browser is not trusted to authenticate itself, choose a command, or choose a socket. The client binds only to a numeric loopback address and compares the request `Host` and WebSocket `Origin` exactly with `HERDR_WEB_CLIENT_PUBLIC_ORIGIN`.

The Go server authenticates the assertion, checking the configured issuer, exact audience, expiration, and RS256 signature using OIDC discovery or the configured JWKS URL. `/api/session` issues a short-lived nonce tied to the authenticated subject. `/api/attach` accepts one WebSocket with subprotocol `herdr-web-client.v1`; its first message must contain that nonce and terminal dimensions. A nonce is single-use, and only one attachment and PTY may be active in a process.

The service and `herdr` process run as the same operating-system user. The PTY child is started directly with the fixed `client` argument, never through a shell. The Herdr socket is accessed as that user, and the service does not claim to isolate a user from other processes already trusted with that account.

The HTTP surface is deliberately small:

- `GET /api/session` requires the session marker `X-Herdr-Web-Client-Request: session` and one valid assertion header. It returns the authenticated email (when supplied by the provider), a nonce, and its expiry.
- `GET /api/attach` upgrades to the WebSocket after exact-origin, assertion, subprotocol, and nonce checks. The existing Herdr message schemas and Herdr API methods/events are kept unchanged.
- Other GET/HEAD requests serve authenticated, embedded static assets. Unknown `/api/` paths are not static fallbacks.

## Requirements and support matrix

| Area | Supported requirement | Boundary of the support claim |
| --- | --- | --- |
| Operating system | Linux host | Published archives are only Linux `amd64` and `arm64`. Other operating systems and architectures are not release targets. |
| Service manager | Per-user systemd 254 or newer with transient user services, namespace hardening, `/usr/bin/systemd-run`, and `/usr/bin/systemctl` (`herdr-web-client.service`) | Every supported attachment is a separate hardened transient service. Non-FHS systemd layouts require a source change. Running the foreground binary under another supervisor uses process-group teardown and is diagnostic only, not a compatibility promise. |
| Herdr integration | An executable at `HERDR_WEB_CLIENT_HERDR_PATH`, a same-user PTY, and a same-user Herdr Unix socket | The client launches only `herdr client`; it does not install or manage Herdr. |
| Browser | Playwright Chromium is the automated compatibility target | Other browser engines, embedded web views, and mobile browsers have no compatibility guarantee. |
| Build toolchain | Go 1.27, Bun exactly 1.3.14, and ShellCheck exactly 0.10.0 for complete verification | Release users do not need these tools. Source builders must use the pinned Bun version and lockfile; `scripts/verify-full` also requires the stated ShellCheck version and Playwright's Chromium dependencies. |
| Authentication | An HTTPS reverse proxy satisfying the OIDC assertion contract below | The application does not provide an interactive login page or a provider-specific proxy configuration. Cloudflare Access is an example, not a requirement. |
| Containerization | None | No Docker image, Kubernetes manifest, or container security boundary is provided. |

## Install a published release

Published archives contain the `herdr-web-client` binary, `README.md`, `LICENSE`, `THIRD_PARTY_NOTICES.md`, `.env.example`, the user unit `herdr-web-client.service` (from `deploy/herdr-web-client.service`), and a `LICENSES/` directory with the exact notices for the linked Go modules and runtime. Archives are named `herdr-web-client_<version>_linux_<arch>.tar.gz`; `SHA256SUMS` is published beside them.

The following uses GitHub CLI and installs into the current user's home directory. Set `VERSION` to a published release tag without its leading `v`.

```sh
(
set -eu
VERSION=1.2.3
TAG="v$VERSION"
if [ "$(uname -s)" != Linux ]; then
  printf 'Unsupported operating system: %s\n' "$(uname -s)" >&2
  exit 1
fi
case "$(uname -m)" in
  x86_64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) printf 'Unsupported architecture: %s\n' "$(uname -m)" >&2; exit 1 ;;
esac

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
gh release download "$TAG" \
  --repo carter2099/herdr-web-client \
  --pattern "herdr-web-client_${VERSION}_linux_${ARCH}.tar.gz" \
  --pattern SHA256SUMS \
  --dir "$work"
(
  cd "$work"
  sha256sum -c SHA256SUMS --ignore-missing
)
mkdir -p "$work/unpacked" "$HOME/.local/bin"
tar -xzf "$work/herdr-web-client_${VERSION}_linux_${ARCH}.tar.gz" -C "$work/unpacked"
archive_root="$work/unpacked/herdr-web-client_${VERSION}_linux_${ARCH}"
install -m 0755 "$archive_root/herdr-web-client" "$HOME/.local/bin/herdr-web-client"
install -Dm644 "$archive_root/herdr-web-client.service" \
  "$HOME/.config/systemd/user/herdr-web-client.service"
)
```

Continue with the configuration and systemd steps below. Do not expose the loopback listener directly to the network.

## Install from source

Source installation is useful when testing a change or when a published archive is not suitable for the host.

```sh
git clone https://github.com/carter2099/herdr-web-client.git
cd herdr-web-client
bun install --frozen-lockfile
mkdir -p "$HOME/.local/bin"
scripts/build "$HOME/.local/bin/herdr-web-client"
```

`scripts/build [output]` first rebuilds the tracked browser bundle in `web/dist`, then builds the Go executable with `-trimpath -buildvcs=false`. With no argument the output is `build/herdr-web-client`. The script also accepts the release version linker flags used by the release workflow; use the script rather than replacing it with an ad-hoc build.

The source tree is not a substitute for configuration. Complete the environment and reverse-proxy steps before starting the executable.

## Configuration

Only the `HERDR_WEB_CLIENT_` namespace is supported. There are no unauthenticated defaults for the public origin, issuer, audience, or assertion header. An explicitly present empty value is an error; do not rely on an empty value to select a default. Values are read by the user service from `~/.config/herdr-web-client/env` or exported for a foreground run.

| Variable | Required / default | Validation and behavior |
| --- | --- | --- |
| `HERDR_WEB_CLIENT_LISTEN_ADDR` | Optional; `127.0.0.1:8080` | Must be `host:port` with a numeric loopback IP and a port from 1 through 65535. Hostnames, wildcard addresses, and non-loopback addresses are rejected. |
| `HERDR_WEB_CLIENT_PUBLIC_ORIGIN` | Required; no default | Must be an exact, browser-canonical HTTPS origin with a lowercase ASCII DNS name or canonical IP literal and no user info, path, query, or fragment. Omit the default `443` port and port leading zeroes. The exact origin's host is also required in `Host`; include a non-default public port here when one is genuinely part of the public origin. |
| `HERDR_WEB_CLIENT_OIDC_ISSUER` | Required; no default | Must be an absolute HTTPS issuer URL. A tenant path is allowed; user info, query, and fragment are not. The value is checked against the JWT issuer claim. |
| `HERDR_WEB_CLIENT_OIDC_AUDIENCE` | Required; no default | Must be nonblank, exact, and free of surrounding whitespace. It is checked against the JWT audience claim. |
| `HERDR_WEB_CLIENT_OIDC_ASSERTION_HEADER` | Required; no default | Must be a legal HTTP field-name token. `Host`, `Origin`, `Cookie`, `X-Herdr-Web-Client-Request`, HTTP connection/framing fields (`Connection`, `Upgrade`, `Content-Length`, `Transfer-Encoding`, `Trailer`, `TE`, `Keep-Alive`, `Proxy-Connection`), and WebSocket handshake fields (`Sec-WebSocket-Key`, `Sec-WebSocket-Version`, `Sec-WebSocket-Protocol`, `Sec-WebSocket-Extensions`) are reserved and rejected case-insensitively. Each request must contain exactly one nonblank JWT value in this header. |
| `HERDR_WEB_CLIENT_OIDC_JWKS_URL` | Optional; discovery from the issuer | When present, must be an absolute HTTPS URL and is used as the remote key set. Issuer, audience, expiration, and RS256 checks still apply. When absent, standard OIDC discovery obtains the key set from the issuer; the discovered URL must also be absolute HTTPS. OIDC redirects cannot downgrade to HTTP, and discovery/key responses are bounded. |
| `HERDR_WEB_CLIENT_HERDR_PATH` | Optional; `$HOME/.local/bin/herdr` | Must be an absolute path to the same-user Herdr executable. It is executed directly with the fixed `client` argument. |
| `HERDR_WEB_CLIENT_HERDR_WORKDIR` | Optional; `$HOME` | Must be an absolute directory used as the PTY child's working directory. |
| `HERDR_WEB_CLIENT_HERDR_SOCKET` | Optional; `$HOME/.config/herdr/herdr.sock` | Must be an absolute Unix-socket path for completion events. The socket and its parent directory must be accessible to the service user. |

The defaults for the three Herdr paths are derived from the account running the process. They never contain a machine-specific user or home directory. The child environment is filtered by the server; do not assume that every service-manager variable is passed to `herdr client`.

A minimal environment file has this shape (use values belonging to the deployment; the names and syntax are the important part):

```dotenv
HERDR_WEB_CLIENT_PUBLIC_ORIGIN=https://terminal.example
HERDR_WEB_CLIENT_OIDC_ISSUER=https://identity.example/tenant
HERDR_WEB_CLIENT_OIDC_AUDIENCE=herdr-web-client
HERDR_WEB_CLIENT_OIDC_ASSERTION_HEADER=Proxy-Auth-Assertion
# HERDR_WEB_CLIENT_OIDC_JWKS_URL=https://identity.example/tenant/keys
# HERDR_WEB_CLIENT_LISTEN_ADDR=127.0.0.1:8080
# Leave the HERDR_WEB_CLIENT_HERDR_* path variables unset to use the
# service user's runtime-derived defaults. If set, use absolute paths.
```

Systemd environment files do not perform shell parameter expansion: a value containing `$HOME` remains literal. Leave the optional path variables unset to use the runtime-derived defaults, or set explicit absolute paths.

## Reverse-proxy and OIDC contract

The application expects an already-authenticated request. It is not an OIDC browser client. Configure a reverse proxy or authentication gateway that does all of the following:

1. Terminates public TLS and forwards only to the loopback listener.
2. Authenticates the user with the chosen identity provider and obtains a signed OIDC JWT.
3. Removes every client-supplied copy of the configured assertion header, then injects exactly one header with exactly one JWT value. Never concatenate duplicate values.
4. Preserves the configured public `Host` exactly. For WebSocket requests it preserves the browser's exact `Origin` and forwards the `Upgrade` request with exactly one `Sec-WebSocket-Protocol: herdr-web-client.v1` value.
5. Allows the browser's `X-Herdr-Web-Client-Request: session` marker on `GET /api/session`, but does not treat that marker as authentication.
6. Does not make the loopback listener, the assertion header, or the Herdr socket publicly reachable.

The application verifies issuer, audience, expiration, and RS256 signature. Configure the identity provider and proxy so the JWT's issuer and audience exactly match the environment values. If an explicit `OIDC_JWKS_URL` is used, it must be reachable by the service and must serve the keys for the configured issuer.

### Cloudflare Access as one example

Cloudflare Access can satisfy this contract when its policy authenticates the user and its signed assertion is injected under the configured header. For that provider only, a deployment may use:

```dotenv
HERDR_WEB_CLIENT_OIDC_ASSERTION_HEADER=Cf-Access-Jwt-Assertion
```

The provider's issuer, audience, and key URL are still deployment-specific and must be configured explicitly. This example does not make Cloudflare Access mandatory and does not grant permission to trust an unstripped client header.

## Same-user systemd deployment

The supplied `deploy/herdr-web-client.service` is a **user** unit. Install and run it as the account that owns the Herdr executable, PTY, and Unix socket; do not install it as a system service running as root. The published-release commands above already install the archive's copy of this unit; from a source checkout, install it with:

```sh
install -Dm644 deploy/herdr-web-client.service \
  "$HOME/.config/systemd/user/herdr-web-client.service"
```

For either installation method, preserve or create the private environment file, then reload and restart the service:

```sh
install -d -m 0700 "$HOME/.config/herdr-web-client" "$HOME/.config/herdr"
if [ ! -e "$HOME/.config/herdr-web-client/env" ]; then
  install -m 0600 /dev/null "$HOME/.config/herdr-web-client/env"
fi
chmod 0600 "$HOME/.config/herdr-web-client/env"
"${VISUAL:-${EDITOR:-vi}}" "$HOME/.config/herdr-web-client/env"
systemctl --user daemon-reload
systemctl --user enable herdr-web-client.service
systemctl --user restart herdr-web-client.service
"$HOME/.local/bin/herdr-web-client" --version
systemctl --user status herdr-web-client.service
```

The same commands are the update path: installing a new archive preserves the existing environment file, and `restart` replaces an already-running process. To roll back, reinstall the previously verified archive's binary and unit, run `daemon-reload`, restart the service, then check both `--version` and service status again.

If the service must continue without an interactive login, enable user-manager lingering according to the host's systemd policy:

```sh
loginctl enable-linger "$USER"
```

The unit's environment file is optional at the systemd layer, but the application still requires the four authentication/public-origin variables. Keep the environment file mode `0600`, keep the Herdr socket directory private, and make sure the proxy can reach the loopback listener while external clients cannot.

To run without systemd while diagnosing a deployment:

```sh
set -a
. "$HOME/.config/herdr-web-client/env"
set +a
"$HOME/.local/bin/herdr-web-client"
```

Do not paste JWTs or cookies into a shell transcript or a support issue.

## Build and verification

Use the pinned toolchain for source work:

```sh
bun --version       # 1.3.14
# Go 1.27 is required by go.mod
shellcheck --version  # 0.10.0, for verify-full
bun install --frozen-lockfile
bunx --no-install playwright install --with-deps chromium  # once, for verify-full
scripts/build
scripts/verify
```

To embed a release version in a binary, pass it through the build script:

```sh
VERSION=v1.2.3 scripts/build "$HOME/.local/bin/herdr-web-client"
```

The verification commands are intentionally layered:

- `scripts/verify` is the fast deterministic layer: repository hygiene, Bun/Go dependency and lockfile checks, Biome and Knip checks, Bun unit tests, Go vet/tests, generated-bundle drift checks, and a production build.
- `scripts/verify-full` is the complete deterministic layer: the fast checks plus pinned ShellCheck, staticcheck, and workflow validation, race testing, tagged compilation, the fixed protocol property corpus, current-and-history gitleaks scanning, deterministic Linux `amd64`/`arm64` release packaging and sandbox installation, and Chromium E2E against the exact native artifact built on the verification host.
- The browser layer (`scripts/verify-browser`) launches the exact supplied binary and exercises only its embedded bundle through a local TLS authentication proxy with an ephemeral CA, OIDC issuer/JWKS fixture, fake `herdr client` PTY, and real Unix-socket JSON-RPC. It does not add a production bypass route. Install the pinned Playwright Chromium build once with the command above before running it locally.

Run `scripts/verify` before each commit and before opening a pull request. Run `scripts/verify-full` before a release and when changing authentication, protocol, PTY, socket, embedding, or deployment behavior. Pull requests and `main` run the required single stable `CI / verify` check, with parallel diagnostic jobs beneath that aggregator; its fast job runs the pinned current-tree and reachable-history secret scan before verification. A nightly security workflow runs current vulnerability feeds and extended randomized fuzzing; those mutable checks are intentionally not PR merge gates.

## Releases

A release is driven through GitHub CLI rather than by manually uploading an archive:

```sh
gh auth status
jq --version
git status --short
scripts/verify-full
scripts/release v1.2.3
```
`scripts/release vMAJOR.MINOR.PATCH[-suffix]` requires the latest completed `main` CI run for the exact source commit to be successful, then dispatches and watches `release.yml` with `gh` for `carter2099/herdr-web-client`. The release workflow repeats the exact-main and CI checks, creates and transactionally rolls back its tag and draft on any pre-publication failure, verifies the exact asset set and SHA-256 digests, and publishes only after those checks pass. A post-publication verification failure leaves the public release and tag intact for explicit maintainer handling rather than deleting artifacts consumers may already have downloaded. Use a suffix such as `-rc.1` for a pre-release when appropriate.

For each Linux architecture, the archive and binary naming is:

```text
herdr-web-client_<version>_linux_amd64.tar.gz
herdr-web-client_<version>_linux_arm64.tar.gz
herdr-web-client
```

Before installation, verify `SHA256SUMS` and inspect the archive contents. Do not replace a running binary until the checksum and architecture are confirmed.

## Security model

The following controls are part of the design, not optional hardening:

- **Network boundary:** listener binds only to a numeric loopback IP; public TLS and access policy belong at the reverse proxy.
- **Request binding:** exact `Host` and exact WebSocket `Origin` must equal the configured HTTPS origin; security headers are sent on HTTP responses.
- **Authentication:** one assertion header value is required; issuer, audience, expiry, and RS256 signature are checked through OIDC discovery or an explicit HTTPS JWKS URL. Missing, duplicate, comma-joined, or whitespace-padded values fail closed.
- **Session binding:** a short-lived random nonce is tied to the JWT subject and expiry, consumed once, and required as the first WebSocket message.
- **Resource limits:** one active attachment, bounded inbound messages and output queues, handshake/read/write deadlines, ping/pong timeouts, and normalized terminal dimensions limit abuse and backpressure.
- **Process boundary:** the server invokes the configured absolute Herdr path with `client` as its only application argument, never through a shell, and filters the child environment. Under the supplied systemd unit, the user manager starts each attachment in a separate transient service whose cgroup controls and user-manager sockets are inaccessible to the child; teardown stops that service with `KillMode=control-group`, covering descendants that change process group or session. At launch, the transient mount namespace also masks every current top-level `/dev` entry except the PTY and standard pseudo-device set. Foreground diagnostic runs fall back to immediate process-group teardown.
- **Local boundary:** PTY, transient-service, Unix-socket, and device permissions are same-user operations. File permissions and the user service manager are part of the threat model. The user unit's device path masks are defense in depth, not a complete device sandbox.

The trusted components are the host kernel/systemd user manager, the configured identity provider, the reverse proxy that enforces the assertion contract, and the same-user Herdr installation. A browser, an arbitrary remote client, or an unvalidated header is not trusted. A local process already able to act as the service user is outside the isolation guarantee.

## Troubleshooting

### The service exits immediately

Inspect the user journal and the service status:

```sh
systemctl --user status herdr-web-client.service
journalctl --user -u herdr-web-client.service -e --no-pager
systemctl --user show herdr-web-client.service -p ExecMainStatus -p ExecMainCode
```

Startup fails closed when a required variable is absent or empty, a URL is not HTTPS, a public origin contains a path/query/fragment, the listener is not numeric loopback, an assertion header is reserved/invalid, or a configured path is not absolute. Check the spelling and prefix of every variable; old environment names are not aliases.

### The browser gets 401, 403, or 404

- `401` normally means the proxy did not inject exactly one configured JWT header, the JWT is expired, or issuer/audience/signature validation failed. Check the proxy's header rewrite and the service's issuer/JWKS reachability without logging token contents.
- `403` normally means the WebSocket `Origin` or the `/api/session` marker is missing or does not match exactly. Do not disable origin checks to work around a proxy rewrite.
- `404` can mean the forwarded `Host` is missing or differs from the configured public origin. Preserve the public host exactly.
- Confirm the proxy forwards `X-Herdr-Web-Client-Request: session` for `/api/session` and does not duplicate the assertion header.

### The WebSocket does not attach

Confirm that the proxy supports WebSocket upgrades, forwards exactly `herdr-web-client.v1`, and preserves the public `Origin`. A stale, reused, subject-mismatched, or expired nonce is rejected. Only one attachment can be active; a second browser receives a conflict until the first session closes.

### The terminal starts but has no output or exits

Run `herdr` manually as the same user and verify that `HERDR_WEB_CLIENT_HERDR_PATH` is executable, `HERDR_WEB_CLIENT_HERDR_WORKDIR` exists, and the account can allocate a PTY. Check that the Herdr socket exists at `HERDR_WEB_CLIENT_HERDR_SOCKET`, its parent is accessible to the service user, and Herdr is producing the expected completion events. The web client does not launch a shell or repair a broken Herdr installation.

### The service works in a shell but not under systemd

Compare the shell's effective paths with the environment file. User units have a deliberate working directory and a filtered child environment. Use absolute paths, keep the env file readable only by the user, reload the user manager after unit changes, and inspect the journal rather than adding secrets to debug output.

## Limitations and known gaps

- Only Linux host deployment is supported by the published artifacts, and only `amd64` and `arm64` archives are produced.
- The supported deployment is a same-user systemd service. There is no Docker/container image, root/system service recipe, or claim that a container can safely provide the required PTY and Unix socket semantics.
- Per-user systemd cannot enforce `PrivateDevices` on every supported host. The parent masks common physical-device paths, and each attachment masks non-pseudo `/dev` entries present when it starts; a nonstandard device path or a new top-level device created after attachment startup can still inherit the service user's access. Use a separately engineered system service/account boundary if complete physical-device isolation is required.
- One process permits one active browser attachment and one Herdr PTY. There is no multi-user session broker, shared nonce store, or horizontal-scale coordination.
- An external reverse proxy and OIDC-aware authentication policy are required. There is no built-in login flow, anonymous mode, or provider-specific configuration beyond the generic signed-header contract.
- OIDC verification currently requires RS256 and a reachable discovery endpoint or explicit HTTPS JWKS URL. Key rotation, network policy, and provider availability remain deployment concerns.
- Playwright Chromium is the tested browser surface. Other engines and embedded/mobile browser environments are not promised to work.
- The listener is intentionally loopback-only, so direct remote access without a correctly configured proxy is unsupported rather than a fallback mode.

## License and notices

The project is MIT-licensed; bundled browser code and linked third-party dependencies retain their own licenses. See [LICENSE](LICENSE) and [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md). Generated release archives carry the exact linked-module and Go runtime licenses under `LICENSES/`.

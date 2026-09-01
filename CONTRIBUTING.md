# Contributing to Herdr Web

Thank you for improving the `herdr-web-client` source. Contributions should keep the browser product **Herdr Web**, the host-only deployment model, and the security boundaries documented in [README.md](README.md) intact.

## Before you start

- Use a Linux development host when changing PTY, Unix-socket, systemd, or release behavior. The published support target is Linux `amd64` and `arm64`.
- Install Go 1.27, Bun exactly 1.3.14, and ShellCheck exactly 0.10.0. Bun is pinned by `.bun-version` and the package metadata.
- Read the README's access-control responsibility and transport contract before changing request headers, origins, WebSocket upgrades, or session behavior.
- Never add Docker, a system-wide root service, or a browser compatibility claim unless the implementation and verification contract genuinely support it.

## Keep private data out of Git

Do not commit or paste any of the following, even in tests, fixtures, screenshots, logs, examples, commit messages, or pull requests:

- passwords, access tokens, cookies, private keys, certificates, signing material, or other secrets;
- private hostnames, private IP addresses, internal domains, internal usernames, home-directory paths, or production URLs;
- real access-control, network, or service-manager configuration;
- generated logs or browser traces that contain private user or deployment data.

Use generic values such as `https://terminal.example` and ephemeral test credentials. Keep local `.env` files untracked. A value that is safe for a local machine is not automatically safe to publish. If a secret or private host is accidentally staged, stop, remove it from the history, and report it privately using the process in [SECURITY.md](SECURITY.md); do not merely delete the working-tree copy.

## Build the browser bundle

`web/dist` is tracked generated output and is embedded into the Go executable. A web-source change is incomplete without regenerated output.

```sh
bun install --frozen-lockfile
scripts/build
```

Review the resulting `web/dist` diff. Do not hand-edit minified bundle files. Keep the document title exactly `Herdr Web`, preserve the `herdr-web-client.v1` WebSocket subprotocol, and keep the existing route and message schemas compatible unless the change explicitly updates the whole contract and its consumers.

If the generated bundle changes, include those generated files in the same pull request as the source change. Do not commit `node_modules`, local build products, credentials, or private test artifacts.

## Verification

Run the fast deterministic checks before opening a pull request:

```sh
scripts/verify
```

Run the complete deterministic checks before requesting review for protocol, PTY, socket, embedding, deployment, or release changes. Install the pinned browser once first:

```sh
bunx --no-install playwright install --with-deps chromium
scripts/verify-full
```

`verify-full` includes pinned static analysis and workflow validation, race testing, tagged compilation, secret scanning, deterministic release-package and sandbox-install checks, and Chromium E2E against the exact built binary. The E2E must exercise that binary and its embedded bundle through the local TLS fixture; do not replace it with a test-only bypass route. Current vulnerability feeds and extended randomized fuzzing run nightly and on manual dispatch, not as mutable PR gates.

The required pull-request merge status is `CI / verify`; `main` runs the same workflow after merge. Run focused tests while iterating, `scripts/verify` before pushing, and `scripts/verify-full` for the sensitive changes listed above and every release. Do not weaken or skip a layer to accommodate a change.

## Make changes safely

- Prefer small, reviewable commits that change source and generated output together.
- Keep the application authentication-neutral. Operators own access control; do not require a particular identity provider, gateway, or authentication protocol.
- Preserve loopback-only binding, exact `Host`/`Origin` checks, single-use nonces, one active attachment, bounded queues, deadlines, direct `herdr client` execution, and child-environment filtering. Changes to these controls need security-focused tests and README updates.
- Keep the same-user relationship between the service, Herdr executable, PTY, and Unix socket explicit. Do not silently add a privileged helper or shell invocation.
- Use the existing API and test patterns. New tests should exercise observable behavior, boundaries, errors, and transitions rather than implementation details.
- Update `THIRD_PARTY_NOTICES.md` and retain the corresponding license files when adding or changing bundled assets or direct dependencies. Release packaging must continue to collect the exact licenses for every linked Go module and runtime. Do not fold third-party license text into the root MIT terms.
- Do not introduce compatibility aliases for retired binary, module, service, or environment names. Migrate every caller in the same change.

## Pull requests

A useful pull request description states:

1. what user-visible or operator-visible behavior changed;
2. which trust boundary, route, environment variable, or generated artifact is affected;
3. how `scripts/verify` and, when applicable, `scripts/verify-full` were run;
4. whether release notes, notices, or support/limitation text need updating.

Keep screenshots and logs synthetic. Call out any intentionally incompatible protocol or configuration change before review. Do not merge a change that leaves `web/dist` stale, exposes a secret/private host, weakens the documented transport boundaries, or adds an unsupported platform/container claim.

## Releases

Only maintainers run the release command. Release preparation must use the pinned toolchain, a clean `main` checkout, `scripts/verify-full`, and the `gh`-driven workflow described in the README. The workflow verifies the source SHA, exact assets, archive contents, and SHA-256 digests before publication.

## License

By contributing, you agree that your contribution is provided under the root MIT license, subject to the separate licenses and notices for bundled browser code and third-party dependencies in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

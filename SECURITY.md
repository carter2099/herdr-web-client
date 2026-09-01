# Security policy

Herdr Web is a self-hosted terminal attachment. A deployment combines this executable with a Linux host, a user-level systemd manager, a same-user Herdr installation, and access controls chosen by the operator. Report vulnerabilities in the complete boundary, not only in the browser bundle.

## Supported versions

| Version | Security support |
| --- | --- |
| Latest published release | Supported for security triage and fixes. Upgrade to this release before reporting when practical. |
| Older published releases | Not supported for guaranteed backports. Reproduce on the latest release first. |
| Unreleased development commits | Development snapshots; no support or patch commitment. |

The latest release is the security baseline. Release-specific support ends when a newer release supersedes it unless a maintainer explicitly announces otherwise.

## Report privately

Use a **private GitHub Security Advisory** for this repository:

<https://github.com/carter2099/herdr-web-client/security/advisories/new>

Do not open a public issue, pull request, discussion, or chat message for an unpatched vulnerability. If the advisory form is unavailable, email [carter2099@pm.me](mailto:carter2099@pm.me) with a brief request to open a private reporting channel; do not include exploit details or secrets in the initial email.

Please include:

- affected release or commit and the deployment shape (Linux architecture, user service or foreground process, how access is restricted, and whether a forwarding layer is involved);
- a concise impact statement and the trust boundary crossed;
- reproducible steps or a minimal synthetic proof of concept;
- expected versus actual behavior, including relevant HTTP status or WebSocket close behavior;
- any mitigation already applied and whether the issue is reproducible on the latest release.

Redact secrets and private deployment data. Never attach real passwords, tokens, cookies, private keys, certificates, private hostnames, internal domains, user home paths, production URLs, or unredacted service/browser logs. Replace them with generic hostnames and ephemeral credentials.

Maintainers will coordinate acknowledgement, severity, a fix, and disclosure timing through the private advisory. There is no guaranteed response or remediation time.

## Deployment security baseline

A secure deployment must satisfy all of these conditions:

- restrict the application to trusted users using controls appropriate for the environment; Herdr Web provides no authentication or user authorization of its own;
- bind the service to a numeric loopback address and expose it only through a network path you control;
- serve the configured exact HTTPS origin and preserve its `Host` and WebSocket `Origin`;
- run the service, `herdr` executable, PTY, and Herdr Unix socket as the same unprivileged user, with private environment and socket directories;
- keep the supplied parent user service's `NoNewPrivileges`, private temporary directory, physical-device path masks, read-only system/home policy, resource limits, restrictive file permissions, namespace restrictions, and read-only cgroup controls unless a documented host requirement justifies a change;
- use systemd 254 or newer and do not weaken the per-attachment transient service's user-manager/device path masks, `ProtectControlGroups`, `RestrictNamespaces`, `KillMode=control-group`, or immediate kill settings;
- keep the service environment file mode `0600` and never put credentials in source or logs.

The server additionally uses exact origin/host checks, single-use nonces, one active attachment, bounded queues, deadlines, fixed-argument process execution, child-environment filtering, and systemd-owned per-attachment service teardown in the supported deployment. Foreground diagnostic runs use process-group teardown instead. These controls do not authenticate the caller, do not isolate a local process that already has the service user's privileges, and do not compensate for an access path open to untrusted users.

The device masks are defense in depth rather than a complete device sandbox: a per-user service cannot enforce `PrivateDevices` on every supported host, and a nonstandard path or a top-level device created after an attachment starts can retain the service user's permissions. Deploy a separately engineered system service/account boundary if complete physical-device isolation is part of the threat model.

## Non-security support questions

For configuration and operator issues, consult [README.md](README.md) first. Keep public questions free of credentials, private hosts, internal domains, and production details. A failure caused by a missing required environment value, an origin mismatch, a second attachment, or an unavailable same-user Herdr socket is not by itself a vulnerability; report it privately if you can demonstrate a security boundary bypass.

# Magpie's embedded Cursor backend

Vendored from https://github.com/leookun/cursor-byok at
`31e9f5cbc98a7d28cffcb5efbb9323cd3e143354` (MIT; see LICENSE).
The source project is archived. This fork owns maintenance of this copy.

Only the Rust server, Semble core and Cursor protocols are included. There is
no Tauri application or separate Cursor BYOK installation. Magpie builds and
embeds `magpie-cursor-backend`, launches it as its child, and owns its lifetime.

Local changes are limited to the `managed` module/entrypoint, control routing,
data isolation, private IPC, opt-in initialization, settings/account ownership,
and app shutdown. Model catalog requests have a two-second upstream deadline;
local identity responses and token refresh use the injected identity. The archived Cursor/provider protocol implementation remains
in its original directories to make future protocol fixes traceable.

Invocation: `--data-dir ABS --control-token-file ABS --ready-file ABS`.
The token file contains 32 cryptographically random bytes encoded as 64 hex
characters. Both files and the data directory belong to the current user;
Magpie restricts Windows ACLs and this backend checks Unix token permissions.
The listener binds to `127.0.0.1:0`, persists its actual service port in
`cursor-byok.db`, and writes `{ "port": PORT, "pid": PID }` to the readiness
file. Authenticated `GET /__byok-api__/healthz` confirms it is serving.
Closing the parent's stdin pipe or delivering SIGTERM/Ctrl-C restores owned
Cursor settings and stops active transports. `--version` and `--license` are
side-effect-free; the MIT notice is retained in every compiled backend.

All control requests require `Authorization: Bearer TOKEN`; control requests
with any Origin header are rejected. Cursor renderer origins are accepted only
after the built-in proxy authenticates a known Cursor route using a per-run
derived relay token. Spoofed incoming relay headers are stripped; the backend
consumes this token before protocol tracing or forwarding upstream. Exposed
control endpoints are models CRUD and Cursor harness
status, CA initialization/trust and enable/disable. Startup and status do not create
a CA, change trust, kill Cursor, or change Cursor settings/accounts. Explicit
initialization generates a dedicated `Magpie Cursor Local CA`; an explicit
trust call uses the current-user Windows root store or macOS login keychain.
Linux returns an administrator command. Tab defaults to direct forwarding;
the archived project's public Tab service is never enabled by default.

Backend caches, logs, keys and database live below the supplied Magpie-owned
directory. A settings/account restoration journal preserves previous values
and only restores fields still equal to values written by this backend.
The settings journal remembers every proxy value earlier runs wrote, because
it is saved before Cursor's settings.json and a failed settings write leaves
an earlier run's value there. An unreadable or malformed journal fails closed. The original standalone
Cursor BYOK data directory is neither imported nor modified.

Outbound TLS uses Rustls with native roots so Linux helpers do not require a
particular shared OpenSSL version. Legacy gateway-only TLS cipher suites that
the archived app accepted through native TLS are consequently unsupported.

Tests must run under a temporary HOME/USERPROFILE/APPDATA/XDG_CONFIG_HOME.
Do not initialize or trust a CA in tests against the real user's environment.

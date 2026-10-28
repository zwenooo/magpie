# Cursor managed by Magpie

This fork bundles a headless Cursor-compatible backend. Install Magpie and
Cursor; a separate Cursor BYOK application, Rust toolchain or runtime download
is not required. Models, third-party API credentials and routing stay in
Magpie. The backend comes from the MIT-licensed archived
[`cursor-byok@31e9f5c`](https://github.com/leookun/cursor-byok/tree/31e9f5cbc98a7d28cffcb5efbb9323cd3e143354),
maintained in this fork under `native/cursor-backend`.

## Connect

1. Add your model providers in Magpie and open Cursor once so its profile exists.
2. Quit Cursor before applying its connection so it cannot overwrite newly
   applied account state from an older in-memory session. On **Agents**, use
   the model list beside **Cursor (Magpie)** to choose which
   models to offer, then turn on its connection. Selecting the list while off
   does not start the backend or change certificate/proxy settings. The first connection shows a dialog
   explaining the bundled service, local certificate trust and Cursor proxy
   settings. Cancel leaves those settings unchanged.
3. Approve setup. Magpie extracts and starts the backend, synchronizes the model
   selection, initializes its own local CA, and requests trust for it. Windows
   uses the current user's certificate store; macOS uses the login keychain and
   may request OS approval. Linux currently displays the administrator command
   needed to trust this certificate; run it and connect again.
4. Open Cursor, start a new conversation, and choose a Magpie model.
   Magpie does not forcibly terminate Cursor or discard open work.

When Cursor has no complete account session, the bundled backend provides a
local identity for using configured third-party models. A real Cursor sign-in
is not required for this local connection. Existing complete account sessions
are preserved; an incomplete session is saved in the ownership journal before
the local identity is written. Local identity responses and token refresh stay
consistent with the injected identity. Cursor's online account/cloud features
are separate from local model routing.

If Cursor still shows **Log In / Sign Up**, quit it completely, apply the
connection again in Magpie, and reopen it. Choose the configured Magpie model
explicitly; **Auto** uses Cursor's official selection. The local model catalog
waits at most two seconds for the official catalog before returning local
models, so an unresponsive official endpoint cannot hide configured models.

Magpie owns the backend's lifetime. A normal Magpie exit closes its child and
attempts to restore the Cursor settings still owned by it. A locked or unreadable
profile leaves the ownership journal for recovery on a later connection. After a crash, reconnect or a
saved connection's next synchronization restarts the backend and reconciles
its ownership records. A second Magpie cannot take the same runtime lock.

An update restart closes this backend and waits for its cleanup before launching
the replacement Magpie, which reconnects from the saved connection. If launching
the replacement fails, the current Magpie resumes that connection. While Cursor's
backend is running, updates use the ordinary restart path on every platform,
waiting for active gateway requests instead of starting two Magpie processes
with the same backend lock. A Cursor conversation waiting on a tool is not an
active gateway request; continuing that entire turn through an update is not
guaranteed. Finish such work before restarting Magpie. Cursor itself stays open.

## Disconnect and state

Disconnect stops the proxy, restores previous Cursor proxy/account values that
still equal what Magpie wrote, removes its managed model records, and stops the
child. Later user changes and a real Cursor sign-in are preserved. The trusted
local CA remains installed for future connections; disconnect does not remove
certificate trust or erase saved model/provider configuration in Magpie.

All backend state lives under Magpie's config folder:

```text
cursor-backend/
  approved                 User's explicit local-setup choice
  connected                Completed connection, eligible for restart/sync
  models.json              Ownership of model configurations
  data/                    Backend database, private CA and restore journals
  runtime/<sha256>/         Verified embedded executable extracted for this build
```

The previous prototype used an independently installed Cursor BYOK and
`~/.cursor-byok-v3`. This implementation does not launch, read or modify that
installation or automatically import its records. If it still manages Cursor's
proxy, disable it in that application before connecting Magpie.

## Maintenance and verification

`internal/cursorbyok` owns private control requests, process lifecycle and model
reconciliation. The Agent adapter supplies Magpie's catalog/key and consent.
The Rust backend handles Cursor wire protocols, certificate setup and reversible
settings ownership; its control API requires a random per-run token and rejects
browser-origin requests. It exposes only model and local harness operations.
The old public Tab service and promotional/plugin management APIs are not used.

Each Windows/macOS/Linux release architecture compiles a pinned Rust backend,
checks its machine type, hashes it, embeds it into Magpie and runs a real child
startup/shutdown test in a disposable profile. Android CLI and server-only
Docker builds do not include a desktop Cursor backend. Source-only Go builds
still compile, but cannot connect Cursor until the bundled payload is built.

Tests cover model synchronization, authentication, consent, process ownership,
startup without CA/profile changes, restored settings, incomplete-session
recovery, local identity/refresh consistency, bounded catalog forwarding, and
refusal to overwrite later edits. The archived protocol may need updates for
newer Cursor versions. On Windows with Cursor 3.27.22, applying the connection
with Cursor closed restored the local-identity workspace without a real
sign-in. Metadata routing and a direct Magpie provider request also passed.
A full conversation from the real Cursor UI remains to be verified; those
checks and a passing local backend smoke test are not that evidence.

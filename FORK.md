# zwenooo/magpie

This repository is an independently maintained fork of
[yetone/magpie](https://github.com/yetone/magpie). The upstream license and
attribution remain intact. Our main branch includes Cursor BYOK integration;
upstream's installation links and releases do not include it.

## Local features and boundaries

```text
internal/cursorbyok/             Bundled Cursor process, private API and ownership
native/cursor-backend/           Vendored MIT headless backend maintained by us
internal/agent/cursorbyok.go     Magpie Agent/catalog/key adapter
internal/agent/agents.go         One registration in builtins
internal/update/fork.go          Fork update policy and release identity
internal/provider/fork_presets.go Fork-owned provider presets (CodeZ)
internal/provider/fork_partners.go Partner-feed policy (off by default)
docs/cursor-byok.md              Setup and synchronization contract
build/fork/                     Fork build and upstream merge commands
.github/workflows/fork.yml       Fork integration checks on three platforms
.github/workflows/release.yml    Full native packaging and gated own releases
```

`internal/cursorbyok` accepts plain `Model` and `Gateway` values plus explicit
database/journal paths. It does not import the agent, provider, catalog,
gateway or GUI packages. The adapter maps the current upstream catalog and
agent key into those values. Cursor remains a separate editor. Its compatible backend is bundled in Magpie,
and managed by Magpie after explicit setup consent; no second app is installed.

The connection ID remains `cursor-byok`; bundled backend state lives under
`cursor-backend/` in Magpie configuration. External prototype state is not
automatically migrated or connected.
Tests of actual Agent connect/sync/disconnect and gateway forwarding remain
beside the adapter; upstream protocol hash fixtures live in the isolated
package. Small edits in shared agent tests give API-backed clients a real
fixture rather than treating their ownership journal as an editable config.

Translations remain in upstream's existing notice registry, with one entry
per language. Creating a second UI loader solely for one notice would add
more coupling than these five entries. The Go module path remains
`github.com/yetone/magpie`, so upstream imports merge without mass rewrites.

CodeZ is an ordinary `KindRelay` preset in `internal/provider/fork_presets.go`.
The upstream preset table includes fork presets through one initializer hook,
so the GUI, CLI and endpoint imports all use the same registration. CodeZ
uses `https://api.codezsy.com` for API traffic and `https://codezsy.com` for
its website and key management; models come from the user's key, not a fixed
list maintained in this fork.

Partner promotions are off by default through `fork_partners.go`. This stops
the feed, cached promotion display, new-partner notices and partner statistics.
Existing provider records are not edited; their cached preset metadata remains
available as an ordinary relay without sponsorship. The explicit
`MAGPIE_PARTNERS` override is retained for private catalogs and isolated tests.
The upstream GUI needs no separate fork layout or deleted partner components.

## Remotes and branches

- `origin`: `https://github.com/zwenooo/magpie.git` (our normal push target).
- `upstream`: `https://github.com/yetone/magpie.git` (read and merge only).
- `main`: our maintained integration branch. Feature branches merge here.

Use normal merge commits for upstream synchronization; do not reset our
main branch to upstream or rewrite published fork history. In a clean
checkout on a local branch:

```powershell
./build/fork/sync-upstream.ps1
```

The script verifies the source remote, fetches `upstream/main`, and merges
it into the current branch. It never pushes. Resolve any reported conflicts
or run `git merge --abort`, then verify before `git push origin HEAD`.
Fetches don't automatically import new upstream tags.

On each merge, pay attention to:

- Keep sponsor banners and promotional links out of both READMEs; retain
  upstream attribution, license notices and privacy disclosures.
- Agent registration, catalog fields, protocol names, key generation and
  the dry-run boundary in the thin adapter.
- The five notice translations and their English template.
- The small fork hooks in `internal/update/update.go` and `notes.go`.
- The fork preset initializer and partner-source/count hooks; CodeZ must remain
  a normal preset, default partner promotions must remain off, and existing
  configured providers must retain their data.
- The own-repository release, Docker and UI preview targets and their
  credential boundaries. The full workflow matrix and publication gates are
  documented in [CI and release builds](docs/ci.md).

## Build and update

Published fork tags use `va.<official>-vb.<fork>`, for example
`va.0.1.1725-vb.0.0.1`. The official component must refer to an official
release actually merged into this branch. Increment the fork component
for our changes on that base; after moving to a new official release,
start its fork component at `0.0.1`. Binary versions normalize this to
`0.1.1725-vb.0.0.1`; download links retain the public tag. See
[release version rules](docs/ci.md#build-versus-publish).

Install the Go version specified by `go.mod`, then use PowerShell 7:

```powershell
./build/fork/build.ps1            # desktop, native Windows build
./build/fork/build.ps1 -NoGUI     # CLI, Windows/Linux/macOS
```

Outputs are named `dist/magpie-fork-<flavor>-<os>-<arch>`. Without a supplied
version they report `fork-<commit>`, so they are clearly source builds.
An uncommitted checkout adds `-dirty` to that version.
Windows desktop builds do not require a separate frontend compilation.
They use Wails' production runtime; the artifact is a standalone executable,
not a signed installer. CI's Linux artifact is the terminal/web-server build.
CI also builds macOS DMG/ZIP and Linux desktop packages on native runners,
both arm64 and amd64, plus all seven upstream CLI targets. See the complete
[artifact and credential matrix](docs/ci.md).

The fork disables the upstream update/notes feeds by default. A release
build must not silently replace itself with the official binary and lose
fork features. Until a fork feed is operated, update by downloading our
CI artifacts or rebuilding this repository. `MAGPIE_UPDATE_FEED` and
`MAGPIE_NOTES_FEED` remain explicit overrides for controlled feeds/tests.
Release-page links point to `zwenooo/magpie`. Branch builds produce artifacts; only a verified version tag and successful
checks permit publication. CI does not operate an automatic update feed.

## Verification

Run tests in the repository's sandbox (`TestMain`/`testenv` isolates agent
files). Pin `GOPATH`, `GOMODCACHE` and `GOCACHE` before changing HOME.

```text
go test -tags nogui ./internal/cursorbyok ./internal/agent ./internal/update -run 'TestCursorBYOK|TestPortMovesEveryAgent|TestDisconnectStaysThroughSync|TestGrokInstalled|TestFork' -count=1
go test -tags nogui ./internal/provider ./internal/stats -run 'TestFork|TestPartners|TestPartner|TestCountPartner|TestNoticePartners|TestSendPartnerCounts' -count=1
node --test internal/gui/tests/fork-providers.test.cjs
go test -race -tags nogui ./internal/cursorbyok ./internal/agent -run '^TestCursorBYOK' -count=1
go vet ./...
GOOS=linux go vet -tags nogui ./...
go build
GOOS=linux go build -tags nogui
go test -tags nogui ./...
```

`GOOS=...` is shell-specific syntax; on PowerShell set `$env:GOOS` for that
command and remove it afterwards. The race detector requires a C compiler.
Notice changes also run `agent-notice-lang.test.cjs` and `i18n-keys.test.cjs`
in Chromium/WebKit and all five languages.

A mock third-party request verifies Magpie routing and usage attribution;
it does not replace a real Cursor Agent conversation. That real-client check
remains outstanding. The former WSL `TestGrokInstalled` failure was a test
fixture executing an MZ placeholder; its fix is kept in a separate commit
and passed 20 race-enabled repetitions.

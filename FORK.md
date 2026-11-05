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
internal/update/fork_github.go   GitHub Releases adapter and channel selection
internal/provider/fork_presets.go Fork-owned provider presets (CodeZ)
internal/provider/fork_partners.go Partner-feed policy (off by default)
internal/sessions/sources.go     Custom read-only session directory registration
internal/gui/sessions_sources.go Session directory scan/import API
internal/library/mcp_folders.go  MCP servers' program and key folders carried by sync
internal/library/claude_dirs.go  The library given to Claude Code's other config folders
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

A command MCP server can keep its program and its keys in the library, so a
sync carries them like a skill's folder (`internal/library/mcp_folders.go`).
It names `{{program}}` (`library/mcp/<name>/program`), `{{private}}`
(`library/mcp/<name>/private`) or `{{state}}` (`mcp-state/<name>`) in its
command, arguments or environment; agents, the check and the servers magpie
runs get this computer's paths. The program is always carried, without what
installing or running it writes. The private folder holds configuration and
keys: it is carried only with the library's keys, as secret env values are,
is never copied into the backups, and is written 0700/0600. The state folder
is never carried. The upstream hooks are small: `Bundle.Folders` and its
calls in `carry.go`, `here` where a server is written or run (`sync.go`,
`mcp_run.go`, `mcp_check.go`, `mcp_copy.go`, `project_mcp.go`), the folders
following a rename or removal in `sync.go`, and `holdsSkills` keeping a
program set aside in `backup.go`.

Claude Code run in config folders of its own (`CLAUDE_CONFIG_DIR`: an account
switcher's, such as ReClaude's `~/.rec/claude/accounts/<id>/config`, or
magpie's saved accounts under `claude-accounts`) gets the library too
(`internal/library/claude_dirs.go`). The last step of every sync gives each
such folder that Claude Code has run in (it has a `.claude.json`), and each
one added by hand (`magpie library claude-dirs add <folder>`), what Claude
Code here was given, as written there: the same MCP entries (under Claude
Code's own lock on that `.claude.json`, the file kept with the sync's
backups first), links to the same library skills, and the same
instructions part in its `CLAUDE.md`. A same-named server there is replaced
with the library's; an account's own skill folder by a skill's name is left
and reported; a link to another copy is replaced. What magpie put in each
folder is recorded in `claude-dirs.json` in magpie's folder, on this
computer only and never carried, so what the library stops giving is taken
out again and the user's own entries are never touched. Hooks, subagents
and tool deny rules stay Claude Code here's. The upstream hooks are one step
at the end of `syncSteps` (`sync.go`) and one after `changeInstructions`
(`instructions.go`); `magpie library claude-dirs` lists the folders and what
each holds of magpie's.

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

Updates and release notes come directly from the public GitHub Releases API
for `zwenooo/magpie`; no separate feed server or GitHub token is required.
The adapter accepts published, non-preview `va.<official>-vb.<fork>` releases
from this repository, requires this computer's own files (its binary, and on
a Mac the app's zip), the notices and the SHA-256 manifest, and passes
verified asset metadata to the existing updater. A release may leave other
systems out: from `va.0.1.3001-vb.0.0.2` releases build only Windows x64,
macOS Apple Silicon and Linux x64.
It does not discover official upstream binaries or privacy-filter releases.
The default channel offers each formal fork release. Stable keeps upstream's
weekly selection: the newest release already three days old at Monday
00:00 UTC, or a newer release marked urgent.

The application's existing automatic-check, automatic-download and restart
settings still control when an update is fetched and installed. Setting
`MAGPIE_UPDATE_FEED=off` or `MAGPIE_NOTES_FEED=off` disables that feed;
explicit URLs keep the existing custom feed formats. Release metadata and
checksums come from GitHub directly through the configured proxy, while an
asset download may use the user's configured mirror and must still match
its SHA-256. See [application updates](docs/ci.md#application-updates).

Releases through `va.0.1.1725-vb.0.0.5` have the old disabled-feed policy.
They need one manual installation of `va.0.1.1725-vb.0.0.6` or a later
update-enabled release. After that, checks and downloads
follow the saved settings. Homebrew CLI installs continue to use
`brew upgrade`; the macOS cask can update from within the app. A version tag (or a
dispatch) builds; pushes to `main` and branches don't. A build-only dispatch
produces artifacts; only a verified version tag and successful checks permit
publication. The release's whole test suite runs on Linux and Windows; macOS
keeps the fork checks and native builds.

## Verification

Run tests in the repository's sandbox (`TestMain`/`testenv` isolates agent
files). Pin `GOPATH`, `GOMODCACHE` and `GOCACHE` before changing HOME.

```text
go test -tags nogui ./internal/cursorbyok ./internal/agent ./internal/update -run 'TestCursorBYOK|TestPortMovesEveryAgent|TestDisconnectStaysThroughSync|TestGrokInstalled|TestFork' -count=1
go test -tags nogui ./internal/provider ./internal/stats -run 'TestFork|TestPartners|TestPartner|TestCountPartner|TestNoticePartners|TestSendPartnerCounts' -count=1
go test -tags nogui ./internal/update -run '^TestFork' -count=1
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

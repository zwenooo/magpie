# zwenooo/magpie

This repository is an independently maintained fork of
[yetone/magpie](https://github.com/yetone/magpie). The upstream license and
attribution remain intact. Our main branch adds these to upstream, and
upstream's installation links and releases include none of them:

- Cursor BYOK: Cursor uses magpie's models through a backend bundled in
  magpie.
- Our own releases and in-app updates (`va.<official>-vb.<fork>` tags).
- The CodeZ preset, and partner promotions off by default.
- Sessions: Claude history folders of any account imported read-only,
  Claude Desktop histories resumed and carried into Claude Code or Codex,
  and any conversation handed to any agent with a prompt.
- Library: MCP servers' program and key folders carried by sync, an MCP
  program updated from a reviewed GitHub commit, and any agent's profile
  (another config folder of it) following that agent's Library, with
  ReClaude's account folders as Claude Code profiles.

It also carries a few fixes to upstream code that upstream doesn't have
([Fixes inside upstream code](#fixes-inside-upstream-code)). Every upstream
file the fork changes is listed, by reason, in
[`build/fork/upstream-edits.txt`](build/fork/upstream-edits.txt).

## Local features and boundaries

```text
internal/cursorbyok/             Bundled Cursor process, private API and ownership
native/cursor-backend/           Vendored MIT headless backend maintained by us
internal/agent/cursorbyok.go     Magpie Agent/catalog/key adapter
internal/agent/cursorbyok_follow.go  The backend's models follow the catalog other processes change
internal/agent/agents.go         One registration in builtins
internal/update/fork.go          Fork update policy and release identity
internal/update/fork_github.go   GitHub Releases adapter and channel selection
internal/provider/fork_presets.go Fork-owned provider presets (CodeZ)
internal/provider/fork_partners.go Partner-feed policy (off by default)
internal/sessions/sources.go     Custom read-only session directory registration
internal/gui/sessions_sources.go Session directory scan/import API
internal/library/mcp_folders.go  MCP servers' program and key folders carried by sync
internal/library/mcp_repo*.go    An MCP program updated from a reviewed GitHub commit
internal/library/profile_follow.go  A profile agent that follows its agent's Library
internal/library/fork_claude_dirs.go  Claude Code folders added by hand; the old claude-dirs records migrated
internal/agent/fork_claude_homes.go  ReClaude's and hand-added Claude Code folders as claude# rows
internal/sessions/handoff.go     A conversation condensed for any agent to carry on
docs/cursor-byok.md              Setup and synchronization contract
build/fork/                     Fork build and upstream merge commands
build/fork/upstream-edits.txt   Every upstream file the fork changes, by reason
.github/workflows/fork.yml       Fork integration checks on three platforms
.github/workflows/release.yml    Full native packaging and gated own releases
.github/workflows/fork-apt-cache.yml  Ubuntu archives for Linux builds, saved on main
.github/actions/fork-apt/         Their install, shared by Release and Test
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

CodeZ serves OpenAI's models natively on its Responses API, so
`forkResponsesFirst` (one call in `Provider.ResponsesFirst`) puts Responses
first for a CodeZ GPT, Codex or o-series model. Agents handed a model's API
(Cursor, Pi, droid, dsh) then ask it on `/v1/responses`, and the gateway's own
translations go there; its other models stay on Chat and Claude on Messages.

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
following a rename or removal in `sync.go`, `holdsSkills` keeping a
program set aside in `backup.go`, and `side` in `mcp_wsl.go` refusing a
server whose program is kept in the library to an agent in WSL.

Such a server can opt in to updates from a GitHub repository
(`internal/library/mcp_repo*.go`; the steps are in
[the library reference](docs/subsystems/library.md#updating-an-mcp-program-from-a-repository-fork)).
`magpie library mcp repo-check` previews a ref's commit, and `repo-update`
installs the commit the user reviewed as the server's program; a private
repository is read through the local GitHub CLI's sign-in, never a token
in the server. The library carries the source and revision, not the
program, so another computer installs it only when asked there. The
upstream hooks are the `Repo*` fields on `Server` (`mcp.go`), `putServers`
keeping the commit this computer installed when a synced copy comes in
(`mcp_local.go`), the `servers/repo-check` and `servers/repo-update`
routes (`internal/gui/library.go`), their commands in `library_cli.go`, and
the server sheet's repository section, with Check for updates, Preview
commit and Apply this commit (`client.js`, `library.css`).

An agent kept in more than one config folder is an agent of its own per
folder upstream: `claude#work` for `~/.claude-work`, `hermes#coder`,
`omp#work`, each ticked on its own. The fork lets such a profile follow its
agent's Library instead (`internal/library/profile_follow.go`): each sync's
first step ticks a follower wherever its agent is ticked and nowhere else,
in every agents list of the library, and the rest of the sync gives it what
it gives any agent, recorded in its own `Applied`. This works for any agent
with profiles; `magpie library follow` lists them and `magpie library
follow <agent> on|off` switches one. One turned off keeps its ticks, its
own from then on; one gone from the computer loses them.

ReClaude's account folders (`~/.rec/claude/accounts/<id>/config`) and
Claude Code folders added by hand (`magpie library claude-dirs add
<folder>`, listed in `claude-dirs.json`) are `claude#` rows too
(`internal/agent/fork_claude_homes.go`, `claude#rec-<id>`), and follow
Claude Code by default. They get what upstream gives a `claude#` row: MCP
servers, skills and instructions; the skill router's slash commands, hooks,
subagents and tool deny rules stay Claude Code's own folder's. Claude Code
keeps a project's disabled servers and Hub entry in the folder's own
`.claude.json`, so a following folder gets Claude Code's there too, with
what magpie wrote kept in `library-follow.json` on this computer only.
magpie's saved accounts (`claude-accounts`) are no rows: the gateway runs
Claude Code there. The fork's earlier sync of these folders recorded what it
wrote in `claude-dirs.json`'s `given`; the first sync after the change hands
that to each folder's own record (`internal/library/fork_claude_dirs.go`),
so nothing is written twice or left behind as the user's, and takes it out
of a folder that is no row.

The upstream hooks are one line in `claudeDirAgents`
(`internal/agent/claude_dirs.go`), a first step in `syncSteps` (`sync.go`),
one before each instructions-only write (`instructions.go`),
`syncClaudeLocal` given its file's path as `syncClaudeLocalIn` and calling
the followers' after Claude Code's (`project_scope.go`), and `RemoveProject`
forgetting their records with Keep (`projects.go`).

A Claude history folder magpie doesn't read on its own (another account's
`CLAUDE_CONFIG_DIR`, a copy from another computer, Claude Desktop's) can be
added as a read-only source on the Sessions page
(`internal/sessions/sources.go`, `internal/gui/sessions_sources.go`): a
scan, after the user allows it, previews up to fifty sessions and imports
the whole folder, and sessions added there later show up too. magpie never
writes a source: an imported session can't be deleted or moved, and a
project move through a source or into one is refused, through links too.
An imported session, and a Claude Desktop one, is resumed where it is
(`claude --resume <id>` in its working folder, `CLAUDE_CONFIG_DIR` naming
its config folder unless that is Claude Code's own, compared by real path),
and Continue in copies it into Claude Code or Codex as a new session. The
upstream hooks are `sourcesForRead` in `Dirs` and `resumeOf` in `assemble`
(`sessions.go`) and `manage.go`, the readers that walk a folder
(`access.go`, `calls.go`, `subagents.go`, `traces.go`, `transcript.go`,
`said_index.go`), `relocate_claude.go` refusing moves, `carry.go` and
`carry_convert.go` for Claude Desktop's copy, and the Sessions plugin's
import dialog (`client.js`, `i18n.js`, `sessions.css`).

Any conversation magpie reads can be carried on in any installed agent
with a prompt, nothing converted (`internal/sessions/handoff.go`). The
Sessions page's Continue in menu lists every installed agent, "Copy the
steps and a prompt": magpie writes the conversation into its `handoffs`
folder twice, whole as the Markdown export, and condensed to at most 60 KB
(15–20k tokens) for an agent to read whole, and copies the steps (open the
session's folder in that agent where it is on this computer, start a new
conversation) and the prompt, in the page's language. A transcript is
mostly tool output and the agent's own records (a 7 MB Claude Desktop
session held 32 KB of words), and an agent given a file reads all of it,
so the condensed record is made by code from the parts every agent's file
is read into, the same for every agent and without a model: the user's
words and the assistant's replies whole, their own headings two levels
down; each run of tool calls one line with its first arguments and the
files they change; no thinking, tool output or what the agent put in
itself (but the prompt Claude Desktop puts after its system reminder).
After a Claude Code compaction it starts at the agent's own summary. When
it is longer, the first turn and the newest stay whole, older ones are
shortened, cut to their first words, then left out; a heading says how it
ended, and where it stopped among tool calls. The prompt has the agent read
it, take what it quotes as material rather than instructions, redo nothing
done, go by the files as they are now, change neither file, and say where
the work stands before going on. Handoffs older than two weeks are removed.
`magpie sessions handoff <agent> <id>` writes one from a terminal. The
upstream hooks are `sessionHandoffRoutes` in `sessions_manage.go`, the
`handoff` case in `sessions_cli.go`, and the menu and its words in the
Sessions plugin (`client.js`, `i18n.js`).

## Fixes inside upstream code

These change upstream's own behaviour, not a fork feature. Each is checked
against upstream's code on every merge; one upstream has fixed, in its own
way or ours, comes out of the fork. Upstream state below is as of
upstream `feb0d8d7f` (2026-11-24).

| Fix | Where | Upstream |
| --- | --- | --- |
| A held empty text block's start goes out before the next Messages block's start, so later deltas and stops for its index refer to a block the client has seen (c9202b01a) | `gateway/textcalls_anthropic.go` | not fixed |
| pf-serve's timeout cause is kept when the reader sees a closed pipe instead of EOF (92b1c5ef7) | `pii/serve.go` | not fixed |
| JSON padding is skipped 1 KiB at a time, so a body padded past 64 MiB doesn't spend the pre-routing deadline under `-race` (ce01f86c0, bb7de239e) | `gateway/fastjson.go` | not fixed |
| `httpServer` gives the serving loop and the handover tests one server, and the comments say an idle keep-alive connection to the old process may close (a8a349eed, 363e7b09d); the drain itself is upstream's | `gateway/gateway.go`, `gateway/handover.go` | test seam only |
| `nextBeat` lets the keep-awake tests drive wakeups by events (e1f42fc0c) | `awake/awake.go` | test seam only |
| The isolated test process pins `GOTMPDIR` to its sandbox's temporary root: Go 1.26's testing takes `GOTMPDIR` over `TMP`, so fixtures landed outside the folder the re-run child's HOME check expects (8362cae64) | `testenv/testenv.go` | not fixed |
| An agent magpie set up whose config still sends every request to magpie's gateway is connected (`Wired`), whatever model it names: Claude Code with a model picked by `/model` that magpie doesn't list showed "not connected · on its Claude subscription" while its `ANTHROPIC_BASE_URL` was magpie's. Claude Code now has `Routed`, as Codex has | `agent/applied.go`, `agent/claude.go` | not fixed |

Taken out because upstream fixed it:

- `summaryTooLarge` reading a size refusal from a 200 stream's error event
  (890c0803b). Upstream 5ee9a1874 fixed #4862 by never keeping magpie's own
  summary ask alive, so its refusal keeps its 413.
- The routing strategies row saying "besides Quota first" (d2f7c3290):
  upstream's 4d92af050 is the same change, merged at feb0d8d7f.

The last group in `build/fork/upstream-edits.txt` is upstream tests changed
so they pass on Windows, in WSL or under `-race`; the code they test is
upstream's.

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

- `./build/fork/check-upstream-edits.ps1` passes: every upstream file the
  fork changes is in `build/fork/upstream-edits.txt`, and nothing listed is
  upstream's again. `sync-upstream.ps1` runs it after a merge without
  conflicts; after resolving conflicts, run it once the merge is committed.
  `fork.yml` runs it on every pull request and every release.
- Each fix in [Fixes inside upstream code](#fixes-inside-upstream-code)
  against upstream's new code: one upstream has fixed comes out.
- Keep sponsor banners and promotional links out of both READMEs; retain
  upstream attribution, license notices and privacy disclosures.
- Agent registration, catalog fields, protocol names, key generation and
  the dry-run boundary in the thin adapter.
- The five notice translations and their English template.
- The small fork hooks in `internal/update/update.go` and `notes.go`.
- The fork preset initializer, the `forkResponsesFirst` call in
  `ResponsesFirst` and partner-source/count hooks; CodeZ must remain
  a normal preset, default partner promotions must remain off, and existing
  configured providers must retain their data.
- The `agent.KeepCursorBackendCurrent` start in `internal/gui/backend.go`'s
  `startBackend`, beside `KeepCodexDaemonCurrent`.
- The GTK/WebKitGTK steps in `release.yml` and `test.yml` use
  `.github/actions/fork-apt`; a changed package list goes into its sets, and
  a changed runner into `fork-apt-cache.yml`'s matrix.
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
CI also builds the Apple Silicon macOS DMG/ZIP and the Linux x64 desktop
package on native runners, plus the terminal builds of the same three
systems (Windows x64, macOS Apple Silicon, Linux x64). See the complete
[artifact and credential matrix](docs/ci.md).

Updates and release notes come directly from the public GitHub Releases API
for `zwenooo/magpie`; no separate feed server or GitHub token is required.
The adapter accepts published, non-preview `va.<official>-vb.<fork>` releases
from this repository, requires this computer's own files (its binary, and on
a Mac the app's zip), the notices and the SHA-256 manifest, and passes
verified asset metadata to the existing updater. A release may leave other
systems out: from `va.0.1.3001-vb.0.0.2` releases build only Windows x64,
macOS Apple Silicon and Linux x64. An x64 GUI or CLI running under Windows
on Arm emulation therefore updates to x64, not an unpublished Arm64 exe.
It does not discover official upstream binaries or privacy-filter releases.
Every version is the default, even on a fresh install of the release that
currently qualifies for Stable. Daily selects the highest formal fork version
published by the most recent 20:00 UTC. Stable selects the highest published
at least 72 hours before Monday 00:00 UTC. A newer urgent release is offered
immediately on either channel; neither channel queries upstream CI or tags.

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
pwsh ./build/fork/check-upstream-edits.ps1
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

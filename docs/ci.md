# Fork CI and release builds

The fork carries the main repository's complete test, Docker, privacy-filter
and UI preview workflows, plus the native packaging workflow adapted from
[`yetone/magpie-releases@d576def`](https://github.com/yetone/magpie-releases/tree/d576def69ef01e62a7f7bb540f37a790eae6f36f).
Source checkout and all publication targets are this fork; no upstream deploy
key or `RELEASES_REPO_TOKEN` is needed. The upstream Go module path remains
unchanged for source compatibility.

## Workflows

| Workflow | Trigger | Result |
| --- | --- | --- |
| Release | Push to `main`/`feat/**`, `v*` tag, or manual dispatch | All native packages and checks; branch pushes build only |
| Test | Called by Release for its resolved SHA, PR, or manual dispatch | Upstream Linux/macOS tests and races, Windows compile/native tests, Linux/Android CLI and site checks |
| Fork integration checks | Called by Release for the same SHA, PR, or manual dispatch | Cursor BYOK and update-policy tests on Linux, macOS and Windows |
| Docker | `main`, `v*`, relevant PR, manual dispatch | Native amd64/arm64 smoke builds; trusted runs publish this fork's GHCR image and provenance |
| privacy-filter | Relevant source changes, `pf-v*`, manual dispatch | Five native helper targets; `pf-v*` publishes a separate prerelease |
| UI preview | Opt-in PR/manual dispatch | Screenshots/video for trusted same-repository PRs, with separate read-only recording and publishing jobs |

The Test workflow retains the upstream Windows-specific checks for update
installation, session moves, usage caches, MCP health, plugin stderr, dsh
and VS Code detection. Windows compiles the full test suite; Linux/macOS
run it. Long suites have an explicit 60-minute package timeout and a
90-minute job bound. Workflow lint, exact release-file validation, release
metadata tests, Homebrew generation checks and UI-preview trust tests are
also run. Full testing can finish after build artifacts become available;
publication waits for it to succeed.

## Main application artifacts

| Platform | Architecture | Files |
| --- | --- | --- |
| macOS | Apple Silicon / arm64 | `magpie-darwin-arm64.dmg`, `magpie-darwin-arm64.zip`, `magpie-cli-darwin-arm64` |
| macOS | Intel / amd64 | `magpie-darwin-amd64.dmg`, `magpie-darwin-amd64.zip`, `magpie-cli-darwin-amd64` |
| Windows | amd64, arm64 | `magpie-windows-<arch>.exe`, `magpie-cli-windows-<arch>.exe` |
| Linux | amd64, arm64 | `magpie-linux-<arch>`, `magpie-cli-linux-<arch>` |
| Android / Termux | arm64 | `magpie-cli-android-arm64` |

That is 15 binaries/archives plus `SHA256SUMS`: the same 16-file contract
as upstream. `build-*` artifacts hold platform outputs; `release-bundle`
contains the complete validated set and `release-notes` its bilingual notes.
Notes are generated deterministically from Git history without a model API.

Mac app bundles use native `make app`, ad-hoc signing by default, and DMG/ZIP
packaging. Linux desktop builds use native Ubuntu 22.04 runners with GTK 3
and WebKitGTK 4.1. Windows builds use upstream's `make release-windows`,
including embedded console launchers, icons, resources and manifests.
Windows and Linux GUI packages must start and serve their gateway on both
architectures. The terminal versions are cross-compiled with cgo disabled.

Windows/Linux desktop files are standalone executables, not MSI/DEB/RPM
installers. Mac ZIP files contain `magpie.app`; DMG files also offer an
Applications link. Ad-hoc Mac packages are not Apple-notarized distributions;
Gatekeeper may require explicit local approval.

## Build versus publish

Pushing a branch runs a build-only release workflow with version
`fork-<commit>`. Download artifacts from Actions. A manual dispatch defaults
to build-only too; `ref` can select a commit already containing this CI.

To publish a version, create a `va.<official>-vb.<fork>` tag on this fork's `main`
and push it, or select an existing matching tag in manual dispatch with
`publish` enabled. All build/check jobs use one resolved SHA. Publication
checks the remote tag again, requires it to identify that SHA and remain
on `main`, verifies every file/hash, uploads a draft, then makes it public
only after the complete 16-file set is present. The `vb` suffix identifies
the fork revision, so these are stable fork releases, not GitHub prereleases.
A published release cannot be
silently overwritten by a rerun; an interrupted draft can be completed.
Both components are three nonnegative integers with no leading zeros.
For example, `va.0.1.1725-vb.0.0.1` is our first revision based on official
`v0.1.1725`. Increase only the fork component for subsequent changes on the
same official release. After merging another official release, update the
official component and start that line at fork `0.0.1`.

The public Git/Release/Docker tag preserves both labels. Binaries and
Homebrew versions use `0.1.1725-vb.0.0.1`, which existing numeric version
parsers accept. macOS uses the official number for its short version and
the fork number for its numeric bundle version; the executable reports the
full normalized version. Windows' numeric resources retain the official
base. Public download/release links map back to the full `va...-vb...` tag.
The workflow fetches the official `v<official>` tag from `yetone/magpie`
and requires it to be an ancestor of the fork source: a label cannot claim
upstream code that has not been merged. `+build`/extra prerelease suffixes
are rejected. An older publication does not replace a newer Latest release.

No tag or public Release is created by merely migrating this CI or running
a branch build. Docker is separate: successful trusted `main` runs publish
`ghcr.io/zwenooo/magpie:edge`; version tags produce a manifest tagged with
the complete public tag, such as `:va.0.1.1725-vb.0.0.1`.

## Optional credentials and services

| Configuration | Purpose |
| --- | --- |
| `MAC_CERTIFICATE_P12`, `MAC_CERTIFICATE_PASSWORD` secrets | Base64 Developer ID Application certificate and password |
| `APPLE_API_KEY_P8`, `APPLE_API_KEY_ID`, `APPLE_API_ISSUER` secrets | App Store Connect notarization authentication |
| Or `APPLE_ID`, `APPLE_APP_SPECIFIC_PASSWORD`, `APPLE_TEAM_ID` | Alternative notarization authentication |
| Repository variable `ENABLE_AI_UI_PREVIEW=true` | Opt in to the AI-driven PR preview |
| `ui-preview` environment secret `DEEPSEEK_API_KEY` | The fork's own model key for preview planning |
| Pages source `ui-previews` branch, root | Serve preview screenshots/videos |

Apple secrets are supplied only for publish runs; branch builds always use
ad-hoc signing. Supplying a certificate without valid notarization credentials
causes the signed release to fail rather than mislabeling it as notarized.
Temporary certificates, keys and keychains are removed on exit.

AI preview skips external forks, deleted sources and closed PRs before
recording or writing a description, including manually selected PRs. The
workflow and scripts remain available without enabling the optional key.
No Discord webhook or upstream notification is configured.

Docker and releases use this repository's normal `GITHUB_TOKEN` with only
the needed job permissions. Workflow settings must allow the required
contents/package writes; no personal token is embedded in the repository.

## Homebrew and helper releases

After a successful stable application Release, `scripts/update-tap.sh`
generates `Formula/magpie.rb` and `Casks/magpie-app.rb` from that release's
six required hashes and commits them to our `main`. Missing or duplicate
checksums fail before writing files. Prereleases and older versions cannot
advance/downgrade the stable tap. No formula pointing to a nonexistent fork
release is checked in before the first release. Once generated, use:

```sh
brew tap zwenooo/magpie https://github.com/zwenooo/magpie
brew install zwenooo/magpie/magpie
brew install --cask zwenooo/magpie/magpie-app
```

The fork's automatic application feed remains disabled; Homebrew users
update through Homebrew. CI availability is separate from feed hosting.

The privacy-filter workflow builds Darwin arm64/amd64, Linux arm64/amd64,
and Windows amd64 helper ZIPs. Its `pf-v*` prereleases are not main-app
releases or Latest. Runtime helper URLs/hashes remain upstream-pinned;
building helper artifacts alone does not change those runtime pins.

## Maintaining upstream parity

Keep the platform matrices and Makefile targets aligned when merging
upstream. Preserve own-repository checkout/publish targets, SHA gates,
read-only PR jobs and secret boundaries. Dependabot checks Actions weekly.
Do not reinstate the old relay into `yetone/magpie-releases`, its install
links, certificate identity, announcement webhook, or generated tap files.

## Initial migration validation

The first native run at `09eed188`
([Actions](https://github.com/zwenooo/magpie/actions/runs/40488071980)) passed
both macOS packaging jobs, both Linux desktop builds, all CLI/Windows
cross-builds, native Windows amd64/arm64 launch checks, the exact 16-file
bundle check, and fork integration checks on all three systems. It provides
`release-bundle` and individual `build-*` download artifacts. This is build
evidence, not a claim that the full application suite has passed.

The prior full Test run at `3087fdc5` exposed two test-fixture defects fixed
separately: provider backup tests created a process-global channel in
separate synctest bubbles; process environment tests inspected the child
before startup had completed. The fixes preserve assertions and production
behavior, with repeated Linux verification.

`TestRestartHandsTheGatewayOver` also reported a connection reset in the
prior run. An isolated HTTP-server reproduction demonstrates an accepted
request can be discarded during `http.Server.Shutdown`; inheriting the
listener alone does not protect that window. Twenty race-enabled repeats
of the integration test passed, so it is intermittent, not resolved.
Its test remains enabled and a failing quality job continues to block a
release. Accepted-connection draining needs a separate runtime fix.

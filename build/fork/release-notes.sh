#!/usr/bin/env bash
# Deterministic bilingual notes: no external model credentials or announcements.
set -euo pipefail
version=${1:?usage: release-notes.sh VERSION_OR_TAG SHA OUTPUT}
sha=${2:?missing source SHA}
output=${3:?missing output path}
scripts=$(cd "$(dirname "$0")" && pwd)
# shellcheck source=release-version.sh
source "$scripts/release-version.sh"
parse_fork_release "$version" || true
previous=$(git describe --tags --match 'va.*-vb.*' --abbrev=0 "$sha^" 2>/dev/null || true)
range=$sha
[[ -z "$previous" ]] || range=$previous..$sha
{
  printf '# magpie fork %s\n\n' "$version"
  printf 'Independent build of [zwenooo/magpie](https://github.com/zwenooo/magpie), including the Cursor BYOK integration.\n\n'
  printf 'Source: [`%s`](https://github.com/zwenooo/magpie/commit/%s).\n\n' "$sha" "$sha"
  if [[ -n "$release_tag" ]]; then
    printf 'Official base: [`v%s`](https://github.com/yetone/magpie/tree/v%s). Fork revision: `%s`. Binary/package version: `%s`.\n\n' "$upstream_version" "$upstream_version" "$fork_version" "$release_version"
    printf 'The `a` component identifies the official release; `b` identifies this fork\x27s revision. This is a stable fork release.\n\n'
  fi
  printf '### Downloads\n\n'
  printf -- '- macOS Apple Silicon / Intel: `magpie-darwin-arm64` / `magpie-darwin-amd64` (`.dmg` or `.zip`).\n'
  printf -- '- Windows x64 / ARM64: `magpie-windows-amd64.exe` / `magpie-windows-arm64.exe`.\n'
  printf -- '- Linux x64 / ARM64: `magpie-linux-amd64` / `magpie-linux-arm64` (GTK 3 and WebKitGTK 4.1 required).\n'
  printf -- '- CLI: `magpie-cli-<os>-<arch>` for macOS, Linux, Windows and Android ARM64 (Termux).\n'
  printf -- '- macOS, Windows and Linux include the Cursor backend; no separate Cursor BYOK installation is needed. Android/Termux does not support desktop Cursor. The bundled source license is in `THIRD-PARTY-NOTICES.txt`.\n'
  printf -- '- Verify downloads with `SHA256SUMS`. Windows/Linux are unsigned; macOS is ad-hoc signed unless this fork has Apple signing and notarization credentials configured.\n\n'
  printf 'This build checks [this fork\x27s GitHub Releases](https://github.com/zwenooo/magpie/releases) for updates and release notes. Automatic checks, downloads and restart follow your existing settings; the Stable channel keeps the weekly three-day waiting period. No separate update server or GitHub token is needed.\n\n'
  printf 'If you use `va.0.1.1725-vb.0.0.5` or earlier, install this download manually once: those versions cannot discover updates. After this installation, updates come from this fork. Homebrew CLI users should continue to use `brew upgrade`.\n\n'
  printf '### Changes / 变更记录\n\n'
  git log --no-merges -50 --format='- %s (`%h`)' "$range" --
  printf '\n<!-- lang:zh -->\n\n### 安装\n\n'
  printf '这是 [zwenooo/magpie](https://github.com/zwenooo/magpie) 独立维护的版本，包含 Cursor BYOK 集成。源代码提交：`%s`。\n\n' "$sha"
  if [[ -n "$release_tag" ]]; then
    printf '官方基线：`v%s`；本 fork 修订号：`%s`；安装包内版本号：`%s`。标签中的 `a` 表示官方版本，`b` 表示我们自己的修订号，是正式版本。\n\n' "$upstream_version" "$fork_version" "$release_version"
  fi
  printf -- '- macOS：Apple Silicon 选择 `darwin-arm64`，Intel 选择 `darwin-amd64`，提供 DMG 和 ZIP。\n'
  printf -- '- Windows：x64 选择 `windows-amd64.exe`，ARM64 选择 `windows-arm64.exe`。\n'
  printf -- '- Linux：选择对应架构的 `magpie-linux-*`；桌面版需要 GTK 3 和 WebKitGTK 4.1。\n'
  printf -- '- 命令行版：`magpie-cli-<系统>-<架构>`，覆盖 macOS、Linux、Windows 和 Android ARM64（Termux）。\n'
  printf -- '- macOS、Windows、Linux 已内置 Cursor 后端，无需单独安装 Cursor BYOK；Android/Termux 不支持桌面 Cursor。内置源码的许可证见 `THIRD-PARTY-NOTICES.txt`。\n'
  printf -- '- 使用 `SHA256SUMS` 校验下载文件。Windows/Linux 未签名；macOS 默认临时签名，配置本仓库的 Apple 凭据后才启用正式签名和公证。\n\n'
  printf '本版本直接从[我们自己的 GitHub Releases](https://github.com/zwenooo/magpie/releases)检查更新和读取更新说明，无需额外更新服务器或 GitHub Token。自动检查、下载和重启仍遵循现有设置；稳定渠道保留每周选择已发布至少三天版本的规则。\n\n'
  printf '如果正在使用 `va.0.1.1725-vb.0.0.5` 或更早版本，需要手动下载安装本版本一次，因为旧版尚未启用更新源。此后按设置获取本 fork 的后续更新。Homebrew CLI 仍使用 `brew upgrade`。变更提交见上方列表。\n'
} > "$output"

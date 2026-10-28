package update

import (
	"errors"
	"regexp"
	"strings"
)

// Keep this fork's distribution policy here so upstream update machinery
// can merge unchanged. No default feed may replace it with an upstream build.
const (
	forkGitHubAPI  = "https://api.github.com/repos/zwenooo/magpie"
	forkUpdateFeed = forkGitHubAPI + "/releases"
	forkNotesFeed  = forkUpdateFeed
	forkReleases   = "https://github.com/zwenooo/magpie/releases"
)

var (
	errUpdatesDisabled = errors.New("automatic update checks are disabled; download releases from " + forkReleases)
	errNotesDisabled   = errors.New("release notes checks are disabled; read release notes at " + forkReleases)
)

var forkVersion = regexp.MustCompile(`^(?:va\.|v)?((?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)-vb\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*))$`)

// The public tag names both origins; the binary retains a numeric SemVer
// for existing version parsers and native application metadata.
func forkReleaseTag(version string) string {
	version = strings.TrimSpace(version)
	if match := forkVersion.FindStringSubmatch(version); match != nil {
		return "va." + match[1]
	}
	return "v" + strings.TrimPrefix(version, "v")
}

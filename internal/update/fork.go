package update

import "errors"

// Keep this fork's distribution policy here so upstream update machinery
// can merge unchanged. No default feed may replace it with an upstream build.
const (
	forkUpdateFeed = ""
	forkNotesFeed  = ""
	forkReleases   = "https://github.com/zwenooo/magpie/releases"
)

var (
	errUpdatesDisabled = errors.New("this fork has no automatic update feed; download releases from " + forkReleases)
	errNotesDisabled   = errors.New("this fork has no release notes feed; read release notes at " + forkReleases)
)

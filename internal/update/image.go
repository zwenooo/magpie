package update

const ImageRepo = "ghcr.io/zwenooo/magpie"

func Image(version string) string {
	if !forkVersion.MatchString(version) {
		return ""
	}
	return ImageRepo + ":" + forkReleaseTag(version)
}

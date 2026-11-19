package update

import (
	"os"
	"strings"
	"testing"
)

// The image a magpie in a container names for a release is that
// release's own tag, the one docker.yml writes, never a floating tag
// (latest, daily, stable) that may hold another release (fishmagic80 on
// X: Daily found 0.1.4479 and the hint said :latest).
func TestImageIsTheReleasesOwnTag(t *testing.T) {
	for in, want := range map[string]string{
		"0.1.4479-vb.0.0.1":    "ghcr.io/zwenooo/magpie:va.0.1.4479-vb.0.0.1",
		"va.0.1.4479-vb.0.0.1": "ghcr.io/zwenooo/magpie:va.0.1.4479-vb.0.0.1",
		"0.1.4479":             "",
		"edge":                 "",
	} {
		if got := Image(in); got != want {
			t.Errorf("Image(%q) = %q, want %q", in, got, want)
		}
	}
	// docker.yml publishes the validated full fork tag.
	y, err := os.ReadFile("../../.github/workflows/docker.yml")
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(y)
	if !strings.Contains(workflow, `parse_fork_release "$GITHUB_REF_NAME"`) || !strings.Contains(workflow, `TAGS=(-t "$IMAGE:$release_tag")`) {
		t.Error("docker.yml no longer uses the validated fork release tag")
	}
	if !strings.Contains(workflow, `IMAGE: ghcr.io/${{ github.repository_owner }}/magpie`) || ImageRepo != "ghcr.io/zwenooo/magpie" {
		t.Error("the image docker.yml pushes isn't ImageRepo")
	}
}

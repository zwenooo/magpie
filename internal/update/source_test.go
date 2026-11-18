package update

import (
	"testing"

	"github.com/yetone/magpie/internal/settings"
)

func TestUpdateSourceSettings(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	if err := settings.Save(settings.Settings{UpdateSource: "cloudflare"}); err == nil {
		t.Fatal("an unknown source was saved")
	}
	for _, v := range []string{"", "site", "github"} {
		if err := settings.Save(settings.Settings{UpdateSource: v}); err != nil || settings.Load().UpdateSource != v {
			t.Fatalf("%q: %v", v, err)
		}
	}
	from := settings.Settings{Theme: "dark", UpdateSource: "site"}
	from.KeepOwn(settings.Settings{UpdateSource: "github"})
	if from.UpdateSource != "github" || from.Theme != "dark" {
		t.Fatalf("KeepOwn: %+v", from)
	}
}

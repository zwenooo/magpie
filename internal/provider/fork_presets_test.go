package provider

import (
	"slices"
	"testing"
)

func TestForkCodeZPresetAndImport(t *testing.T) {
	p, err := FromPreset("codez")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "CodeZ" || p.Preset != "codez" || p.Icon != "codez" || p.Key != "" || p.Catalog != "" {
		t.Fatalf("CodeZ must be a key-based provider with its own live model list: %+v", p)
	}
	if p.Chat != "https://api.codezsy.com/v1" || p.Responses != p.Chat || p.Anthropic != "https://api.codezsy.com" {
		t.Fatalf("CodeZ protocol bases: chat=%q responses=%q anthropic=%q", p.Chat, p.Responses, p.Anthropic)
	}
	if p.Website != "https://codezsy.com" || p.KeysURL != "https://codezsy.com/console/token" {
		t.Fatalf("CodeZ links: %q %q", p.Website, p.KeysURL)
	}
	list := Presets()
	index := slices.IndexFunc(list, func(pr PresetDef) bool { return pr.ID == "codez" })
	if index < 0 || list[index].Kind != KindRelay || list[index].Sponsored || list[index].NoKey {
		t.Fatal("CodeZ must be offered as an ordinary API relay, with an API key")
	}
	for _, bases := range []endpoints{{chat: p.Chat}, {responses: p.Responses}, {anthropic: p.Anthropic}} {
		im, _ := imported("my-codez", "fixture-key", bases, nil)
		if im.Preset != "codez" {
			t.Fatalf("import did not recognize CodeZ at %+v: %q", bases, im.Preset)
		}
	}
	if Preset("openai").Chat != "https://api.openai.com/v1" || Preset("openrouter").Kind != KindRelay {
		t.Fatal("upstream presets changed")
	}
	for _, icon := range []string{"generic", "", "file:custom-logo.png", "openai"} {
		existing := p
		existing.Icon = icon
		want := icon
		if icon == "generic" || icon == "" {
			want = "codez"
		}
		if got := normalize(existing).Icon; got != want {
			t.Fatalf("existing CodeZ icon %q became %q, want %q", icon, got, want)
		}
	}
}

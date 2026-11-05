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
	// CodeZ asks OpenAI's models on Responses, its others where they were
	for model, want := range map[string]Protocol{
		"gpt-6-astra": Responses, "gpt-5.6-sol": Responses, "o4-mini": Responses, "codex-mini": Responses,
		"claude-opus-4-5": Anthropic, "deepseek-v4": Chat, "codex-auto-review": Responses,
	} {
		if got := p.Native(model); got != want {
			t.Errorf("CodeZ %s is asked on %q, want %q", model, got, want)
		}
	}
	if got := p.Native("gpt-6-astra"); got != Responses || !p.ResponsesFirst("gpt-6-astra") {
		t.Errorf("CodeZ GPT: native %q", got)
	}
	// without a Responses URL, or at a relay that isn't CodeZ, Chat stays first
	chatOnly := p
	chatOnly.Responses = ""
	if chatOnly.ResponsesFirst("gpt-6-astra") || chatOnly.Native("gpt-6-astra") != Chat {
		t.Error("CodeZ without a Responses URL must stay on Chat")
	}
	other := p
	other.Preset, other.ID = "", "my-relay"
	if other.ResponsesFirst("gpt-6-astra") || other.Native("gpt-6-astra") != Chat {
		t.Error("another relay's GPT must stay on Chat")
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

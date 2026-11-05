package agent

import (
	"testing"

	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/provider"
)

// CodeZ serves OpenAI's models on Responses: Cursor is handed a CodeZ GPT
// on /v1/responses, not on the Chat API listed first, while its other
// models keep the API they are served on.
func TestCursorBYOKCodeZGPTOnResponses(t *testing.T) {
	home := syncHome(t)
	f := newCursorBYOKFixture(t, home)
	if err := provider.Save(provider.Provider{ID: "codez", Name: "CodeZ", Preset: "codez", Key: "k",
		Chat: "http://127.0.0.1:1/v1", Responses: "http://127.0.0.1:1/v1", Anthropic: "http://127.0.0.1:1",
		Models: []string{"gpt-6-astra", "deepseek-v4", "claude-opus-5"}}); err != nil {
		t.Fatal(err)
	}
	a, err := Find("cursor-byok")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Connect(); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"codez/gpt-6-astra":   "/v1/responses",
		"codez/deepseek-v4":   "/v1/chat/completions",
		"codez/claude-opus-5": "/v1/messages",
	}
	for _, m := range f.snapshot() {
		id, _ := m["model_id"].(string)
		endpoint, ok := want[id]
		if !ok {
			continue
		}
		delete(want, id)
		if m["base_url"] != gateway.URL()+endpoint {
			t.Errorf("%s is handed %v, want %s", id, m["base_url"], endpoint)
		}
	}
	if len(want) != 0 {
		t.Fatalf("models missing: %v", want)
	}
}

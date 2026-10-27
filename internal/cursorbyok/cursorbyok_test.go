package cursorbyok

import (
	"encoding/json"
	"testing"
)

func TestCursorBYOKHashMatchesUpstream(t *testing.T) {
	// These hashes were emitted by the unmodified Rust model_hash from
	// cursor-byok 31e9f5c for full URLs on each supported protocol.
	for _, tc := range []struct{ kind, endpoint, hash string }{
		{"openai", "/v1/chat/completions", "76bfff6da6661da6"},
		{"openai", "/v1/responses", "5f31ee232338d8b7"},
		{"anthropic", "/v1/messages", "9fe2b26490c19dd5"},
	} {
		t.Run(tc.endpoint, func(t *testing.T) {
			endpoint := tc.endpoint
			if tc.kind == "anthropic" {
				endpoint = ""
			}
			b, err := json.Marshal(map[string]any{
				"display_name": "GLM-4.6", "type": tc.kind,
				"base_url": "http://127.0.0.1:3425" + tc.endpoint, "use_full_url": true,
				"api_key": "magpie-cursor-byok", "model_id": "relay/glm-4.6",
				"openai_endpoint": endpoint,
			})
			if err != nil {
				t.Fatal(err)
			}
			var m modelConfig
			if err := json.Unmarshal(b, &m); err != nil {
				t.Fatal(err)
			}
			if got := hash(m); got != tc.hash {
				t.Fatalf("model identity %s, want upstream's %s", got, tc.hash)
			}
		})
	}
}

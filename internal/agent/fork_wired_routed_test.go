package agent

import (
	"path/filepath"
	"testing"

	"github.com/yetone/magpie/internal/edit"
	"github.com/yetone/magpie/internal/provider"
)

// The owner's Claude Code, 11-24: magpie connected it, then /model in
// Claude Code saved gpt-6.2-astra, a name magpie doesn't list. Its
// settings.json still sent every request to magpie's gateway, and the
// Agents page said "not connected · on its Claude subscription". An agent
// magpie set up whose config still sends to magpie is connected, whatever
// model it names; switched off, magpie's address comes out. One whose
// address was written by hand, magpie never having set it up, isn't.
func TestRoutedAgentMagpieSetUpIsConnected(t *testing.T) {
	home, _ := codexHome(t, "", "")
	if err := provider.Save(provider.Provider{ID: "codez", Name: "CodeZ", Chat: "https://c.example/v1", Key: "k", Models: []string{"claude-opus-5-5", "gpt-6.2-sol"}}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".claude", "settings.json")
	writeFile(t, path, `{}`)
	c := claude(home)
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	// /model in Claude Code saves its pick there
	if err := edit.SetJSON(path, edit.KV{Path: "model", Value: "gpt-6.2-astra"}); err != nil {
		t.Fatal(err)
	}
	c = claude(home)
	if !c.Routed() {
		t.Fatalf("Claude Code no longer sends to magpie:\n%s", readFile(path))
	}
	if !c.Wired() {
		t.Fatal("Claude Code on magpie's gateway, set up by magpie, reads as not connected")
	}
	if err := c.Disconnect(); err != nil {
		t.Fatal(err)
	}
	if u, _ := edit.GetJSON(path, "env.ANTHROPIC_BASE_URL"); u != "" {
		t.Fatalf("switched off, Claude Code still sends to %s", u)
	}
	if claude(home).Wired() {
		t.Fatal("switched off, Claude Code reads as connected")
	}

	// magpie's address written by hand: the user's own, not connected
	writeFile(t, path, `{"model": "gpt-6.2-astra"}`)
	if err := edit.SetJSON(path, edit.KV{Path: "env.ANTHROPIC_BASE_URL", Value: here(home).gw()}); err != nil {
		t.Fatal(err)
	}
	if c := claude(home); !c.Routed() || c.Wired() {
		t.Fatalf("written by hand: routed %v, connected %v", c.Routed(), c.Wired())
	}
}

package agent

import (
	"strings"

	"github.com/yetone/magpie/internal/provider"
)

// claudeOwnName says v is a model Claude Code can ask Anthropic's own
// endpoint for: one of its aliases (opus, sonnet…), or a Claude model's id,
// [1m] or not. A name only magpie serves (gpt-6.2-astra, picked by /model
// while Claude Code was on magpie) is none (zwenooo/magpie fork).
func claudeOwnName(v string) bool {
	v = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(v), "[1m]"))
	return contains(claudeAliases, v) || strings.HasPrefix(v, "claude-")
}

// magpieOnlyName says v is a model's name magpie's gateway serves as it
// takes a bare id (a routing group, else a provider that has it) and
// Anthropic's endpoint doesn't: no Claude model's name. A name of the
// user's that magpie doesn't serve either stays theirs.
func magpieOnlyName(v string) bool {
	if v == "" || claudeOwnName(v) {
		return false
	}
	_, _, served := provider.Resolve(v)
	return served || groupNamed(v) != ""
}

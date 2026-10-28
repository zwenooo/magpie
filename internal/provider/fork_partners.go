package provider

// Keep this fork's promotion policy separate from the upstream feed parser.
// An explicit source remains useful for a private catalog or an isolated test.
func forkPartnersSource(override string) string {
	if override == "" {
		return "off"
	}
	return override
}

// Cached presets still describe providers the user already configured. With
// the feed off they retain their endpoints and regions, without promotion.
func forkUnsponsoredPartner(d PresetDef) *PresetDef {
	d.Kind, d.Sponsored, d.Note = KindRelay, false, ""
	return &d
}

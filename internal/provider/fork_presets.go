package provider

// Fork presets stay separate from upstream's catalog so vendor updates merge
// without mixing our endpoints into its large preset table.
func forkPresets() []PresetDef {
	return []PresetDef{{
		ID: "codez", Name: "CodeZ", Icon: "codez", Kind: KindRelay,
		Chat:      "https://api.codezsy.com/v1",
		Responses: "https://api.codezsy.com/v1",
		Anthropic: "https://api.codezsy.com",
		Website:   "https://codezsy.com",
		KeysURL:   "https://codezsy.com/console/token",
	}}
}

// The first CodeZ preview shipped a generic placeholder. Replace that default
// while keeping uploaded or explicitly selected icons on existing providers.
func forkUsesPresetIcon(p Provider) bool {
	return p.Preset == "codez" && p.Icon == "generic"
}

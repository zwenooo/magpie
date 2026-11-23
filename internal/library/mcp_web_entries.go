package library

import (
	"encoding/json"
	"net/netip"
	"net/url"
	slashpath "path"
	"strconv"
	"strings"
	"unicode"

	"github.com/tidwall/gjson"
	"github.com/yetone/magpie/internal/lazyre"
	"github.com/yetone/magpie/internal/provider"
)

// MCPWebEntry is a page advertised by a server, not a verified service.
type MCPWebEntry struct {
	Kind string `json:"kind"`
	URL  string `json:"url"`
}

func (h ServerHealth) withWebEntries(init json.RawMessage, s *Server, distro string) ServerHealth {
	if h.State != "ok" {
		return h
	}
	// Only the exact names from initialize count, not similarly named fields
	// in capabilities, tool descriptions, or some other vendor's extension.
	ext := gjson.GetBytes(init, `_meta.io\.github\.zwenooo\.magpie/web-entries`)
	var base *url.URL
	local := false
	if s.Transport == "stdio" {
		local = distro == "" && !remoteLauncher(s.Command)
	} else {
		base, _ = url.Parse(s.URL)
	}
	add := func(kind string, v gjson.Result) {
		if v.Type == gjson.String && allowWebEntry(v.Str, kind, local, base) {
			h.WebEntries = append(h.WebEntries, MCPWebEntry{kind, strings.Clone(v.Str)})
		}
	}
	add("website", gjson.GetBytes(init, "serverInfo.websiteUrl"))
	add("console", ext.Get("console"))
	add("logs", ext.Get("logs"))
	return h
}

// A Windows-side command may itself launch the server in another namespace.
func remoteLauncher(command string) bool {
	name := slashpath.Base(strings.ReplaceAll(command, `\`, "/"))
	switch strings.TrimSuffix(strings.ToLower(name), ".exe") {
	case "wsl", "ssh", "docker", "podman", "kubectl":
		return true
	}
	return false
}

var webDNSLabel = lazyre.New(`(?i)^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
var webNumericLabel = lazyre.New(`(?i)^([0-9]+|0x[0-9a-f]*)$`)

func allowWebEntry(raw, kind string, local bool, base *url.URL) bool {
	if strings.ContainsAny(raw, `?#\`) || strings.ContainsFunc(raw, unicode.IsSpace) || strings.ContainsFunc(raw, unicode.IsControl) {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Hostname() == "" || strings.ContainsFunc(u.Path, unicode.IsControl) {
		return false
	}
	if p := u.Port(); p != "" {
		if port, err := strconv.ParseUint(p, 10, 16); err != nil || port == 0 {
			return false
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return false
	}
	if strings.ContainsFunc(u.Host, func(r rune) bool { return r > unicode.MaxASCII }) {
		return false // browsers map some Unicode hostnames to ASCII before dialing
	}
	host := strings.ToLower(u.Hostname())
	ip, err := netip.ParseAddr(host)
	loop := false
	if err == nil {
		if ip.Zone() != "" || ip.Unmap().IsUnspecified() || ip.Is6() != strings.HasPrefix(u.Host, "[") {
			return false
		}
		loop = ip.Unmap().IsLoopback()
	} else {
		// Accept unambiguous ASCII DNS names. Browsers also interpret short,
		// decimal, octal and hex numbers as IPv4, unlike netip and net/url.
		host = strings.TrimSuffix(host, ".")
		labels := strings.Split(host, ".")
		if strings.HasPrefix(u.Host, "[") || len(host) > 253 || webNumericLabel.MatchString(labels[len(labels)-1]) || strings.HasSuffix(host, ".localhost") {
			return false
		}
		for _, l := range labels {
			if !webDNSLabel.MatchString(l) {
				return false
			}
		}
		loop = host == "localhost"
	}
	if loop {
		return local
	}
	return u.Scheme == "https" && (local || kind == "website" || base != nil && base.Scheme == "https" && provider.SameOrigin(base.Scheme, base.Host, u.Scheme, u.Host))
}

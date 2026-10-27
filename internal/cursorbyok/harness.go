package cursorbyok

import (
	"errors"
	"net"
	"net/url"
)

const harnessPath = "/__byok-api__/api/harness/cursor"

// HarnessStatus is the bundled backend's read-only state. A status read
// never installs trust, changes Cursor settings, or starts the proxy.
type HarnessStatus struct {
	CA              string `json:"ca"`
	Integration     string `json:"integration"`
	SettingsApplied bool   `json:"settings_applied"`
	InstallCommand  string `json:"ca_install_command"`
}

func (m Manager) address() (string, error) {
	if m.Control == nil {
		return "", errors.New("Magpie's bundled Cursor backend is not running")
	}
	u, err := url.Parse(m.Control.URL)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Hostname() != "127.0.0.1" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || m.Control.Token == "" {
		return "", errors.New("the managed Cursor backend has an invalid local endpoint")
	}
	if _, _, err := net.SplitHostPort(u.Host); err != nil {
		return "", errors.New("the managed Cursor backend has an invalid local port")
	}
	return m.Control.URL, nil
}

func (m Manager) harness(method, suffix string, body any) (HarnessStatus, error) {
	var status HarnessStatus
	addr, err := m.address()
	if err != nil {
		return status, err
	}
	err = m.ask(addr, method, harnessPath+suffix, body, &status)
	return status, err
}

func (m Manager) Harness() (HarnessStatus, error) { return m.harness("GET", "/status", nil) }
func (m Manager) InitializeCA() (HarnessStatus, error) {
	return m.harness("POST", "/ca/initialize", map[string]any{})
}
func (m Manager) TrustCA() (HarnessStatus, error) {
	return m.harness("POST", "/ca/trust", map[string]any{})
}
func (m Manager) Enable(on bool) (HarnessStatus, error) {
	return m.harness("PUT", "/enabled", map[string]bool{"enabled": on})
}

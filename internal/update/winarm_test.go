package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// onWindows runs the test as a Windows magpie built for arch, under Windows
// on Arm's x64 emulation or not.
func onWindows(t *testing.T, arch string, em bool) {
	t.Helper()
	onSystem(t, "windows", arch, false)
	emulated = func() bool { return em }
}

// An x64 magpie using a custom feed on Windows on Arm updates to the arm64 exe, the
// app and the terminal build alike (@EV_John_Liu on X); an x64 Windows
// computer, and an arm64 build, update to their own. Emulation is
// Windows' alone: a Mac or Linux magpie asked about it is unchanged.
func TestWindowsOnArmUpdatesToArm64(t *testing.T) {
	t.Setenv("MAGPIE_UPDATE_FEED", "https://custom.invalid/latest")
	was := GUI
	t.Cleanup(func() { GUI = was })
	for _, c := range []struct {
		os, arch string
		em, gui  bool
		bin      string
	}{
		{"windows", "amd64", true, true, "magpie-windows-arm64.exe"},
		{"windows", "amd64", true, false, "magpie-cli-windows-arm64.exe"},
		{"windows", "amd64", false, true, "magpie-windows-amd64.exe"},
		{"windows", "amd64", false, false, "magpie-cli-windows-amd64.exe"},
		{"windows", "arm64", false, true, "magpie-windows-arm64.exe"},
		{"darwin", "amd64", true, false, "magpie-cli-darwin-amd64"},
		{"linux", "amd64", true, true, "magpie-linux-amd64"},
	} {
		onSystem(t, c.os, c.arch, false)
		emulated = func() bool { return c.em }
		GUI = c.gui
		if got := BinaryAsset(); got != c.bin {
			t.Errorf("%s/%s emulated=%v gui=%v: BinaryAsset = %s, want %s", c.os, c.arch, c.em, c.gui, got, c.bin)
		}
	}
}

// A current fork release has only the x64 exe for Windows.
func TestForkWindowsOnArmInstallsX64Exe(t *testing.T) {
	t.Setenv("MAGPIE_UPDATE_FEED", "")
	was := GUI
	t.Cleanup(func() { GUI = was })
	onWindows(t, "amd64", true)
	GUI = false
	if got := BinaryAsset(); got != "magpie-cli-windows-amd64.exe" {
		t.Fatalf("emulated CLI: %s", got)
	}
	GUI = true
	wasStart := canStart
	t.Cleanup(func() { canStart = wasStart })
	canStart = func(string) error { return nil }

	bodies := map[string]string{
		"magpie-windows-amd64.exe": "2.0 for x64",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(bodies[strings.TrimPrefix(r.URL.Path, "/")]))
	}))
	defer srv.Close()
	assets := map[string]Asset{}
	for name, body := range bodies {
		sum := sha256.Sum256([]byte(body))
		assets[name] = Asset{URL: srv.URL + "/" + name, Size: int64(len(body)), SHA256: hex.EncodeToString(sum[:])}
	}
	rel := &Release{Version: "2.0", Assets: assets}

	exe := filepath.Join(t.TempDir(), "magpie.exe")
	if err := os.WriteFile(exe, []byte("1.0 for x64"), 0o755); err != nil {
		t.Fatal(err)
	}
	staged, err := stageBinaryFor(context.Background(), rel.Assets[BinaryAsset()], exe)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := InstallBinaryAside(staged, exe); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "2.0 for x64" {
		t.Fatalf("magpie.exe is %q, want the published x64 build", got)
	}
}

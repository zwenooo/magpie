package update

// Adapt only this repository's public releases to upstream's updater contract.
// Download, SHA verification, staging and installation remain in update.go.
import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type forkGitHubRelease struct {
	Tag       string            `json:"tag_name"`
	Body      string            `json:"body"`
	URL       string            `json:"html_url"`
	Draft     bool              `json:"draft"`
	Preview   bool              `json:"prerelease"`
	Published time.Time         `json:"published_at"`
	Assets    []forkGitHubAsset `json:"assets"`
}

type forkGitHubAsset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	State  string `json:"state"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}

var forkReleaseFiles = []string{
	"THIRD-PARTY-NOTICES.txt", "magpie-cli-android-arm64",
	"magpie-cli-darwin-amd64", "magpie-cli-darwin-arm64",
	"magpie-cli-linux-amd64", "magpie-cli-linux-arm64",
	"magpie-cli-windows-amd64.exe", "magpie-cli-windows-arm64.exe",
	"magpie-darwin-amd64.dmg", "magpie-darwin-amd64.zip",
	"magpie-darwin-arm64.dmg", "magpie-darwin-arm64.zip",
	"magpie-linux-amd64", "magpie-linux-arm64",
	"magpie-windows-amd64.exe", "magpie-windows-arm64.exe",
}

func forkNormalizedTag(tag string) string {
	if !strings.HasPrefix(tag, "va.") || !forkVersion.MatchString(tag) {
		return ""
	}
	return strings.TrimPrefix(tag, "va.")
}

func (r forkGitHubRelease) eligible(now time.Time) bool {
	return !r.Draft && !r.Preview && forkNormalizedTag(r.Tag) != "" &&
		!r.Published.IsZero() && !r.Published.After(now) && r.URL == forkReleases+"/tag/"+r.Tag
}

// A bounded request uses the existing proxy settings but never a user's asset
// mirror or account token for authoritative release metadata/checksums.
func forkGet(ctx context.Context, address string, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "magpie-fork-updater")
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	c := *client
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many GitHub update redirects")
		}
		u := req.URL
		if u.Scheme != "https" || u.User != nil || u.Port() != "" {
			return errors.New("invalid GitHub update redirect")
		}
		if strings.HasPrefix(address, forkGitHubAPI+"/") {
			if u.Host != "api.github.com" || !strings.HasPrefix(u.Path, "/repos/zwenooo/magpie/") {
				return errors.New("update metadata redirected outside this fork")
			}
		} else {
			if u.Host == "github.com" {
				if !strings.HasPrefix(u.Path, "/zwenooo/magpie/releases/download/") {
					return errors.New("checksum redirected outside this fork")
				}
			} else if u.Host != "release-assets.githubusercontent.com" && u.Host != "objects.githubusercontent.com" {
				return errors.New("checksum redirected outside GitHub asset storage")
			}
		}
		return nil
	}
	res, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GitHub updates: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		if res.StatusCode == http.StatusTooManyRequests || res.StatusCode == http.StatusForbidden && res.Header.Get("X-RateLimit-Remaining") == "0" {
			return nil, errors.New("GitHub update checks are rate-limited; try again later or open " + forkReleases)
		}
		return nil, fmt.Errorf("GitHub updates: HTTP %d; releases are available at %s", res.StatusCode, forkReleases)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, errors.New("GitHub update metadata exceeds its size limit")
	}
	return b, nil
}

func forkReleaseList(ctx context.Context) ([]forkGitHubRelease, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	var all []forkGitHubRelease
	seen := make(map[string]bool)
	for page := 1; page <= 15; page++ {
		b, err := forkGet(ctx, forkGitHubAPI+"/releases?per_page=100&page="+strconv.Itoa(page), 8<<20)
		if err != nil {
			return nil, err
		}
		var entries []forkGitHubRelease
		if err := json.Unmarshal(b, &entries); err != nil || strings.TrimSpace(string(b)) == "null" || len(entries) > 100 {
			return nil, errors.New("GitHub returned an invalid release list")
		}
		for _, entry := range entries {
			if !entry.eligible(time.Now()) {
				continue
			}
			if seen[entry.Tag] {
				return nil, errors.New("GitHub returned duplicate fork releases")
			}
			seen[entry.Tag] = true
			all = append(all, entry)
		}
		if len(entries) < 100 {
			return all, nil
		}
	}
	return all, nil // matches the existing 1500-release notes retention cap
}

var forkUrgent = regexp.MustCompile(`(?im)^[ \t]*(?:>[ \t]*)?\*\*urgent release:\*\*`)

// Preserve the upstream Stable channel: Monday's newest release already three
// days old, or a newer urgent release. Every-version also excludes previews.
func forkChoose(list []forkGitHubRelease, stable bool, now time.Time) *forkGitHubRelease {
	now = now.UTC()
	monday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -(int(now.Weekday())+6)%7)
	cut := monday.Add(-72 * time.Hour)
	var best *forkGitHubRelease
	for i := range list {
		r := &list[i]
		if !r.eligible(now) || stable && r.Published.After(cut) && !forkUrgent.MatchString(r.Body) {
			continue
		}
		if best == nil || Newer(forkNormalizedTag(r.Tag), forkNormalizedTag(best.Tag)) {
			best = r
		}
	}
	return best
}

func forkLatest(ctx context.Context, lang string, stable bool) (*Release, error) {
	list, err := forkReleaseList(ctx)
	if err != nil {
		return nil, err
	}
	chosen := forkChoose(list, stable, time.Now())
	if chosen == nil {
		return nil, errors.New("no published fork release is available for this update channel; see " + forkReleases)
	}
	return forkRelease(ctx, *chosen, lang)
}

func forkRelease(ctx context.Context, r forkGitHubRelease, lang string) (*Release, error) {
	if !r.eligible(time.Now()) {
		return nil, errors.New("not a published release of this fork")
	}
	base := forkReleases + "/download/" + r.Tag + "/"
	assets := make(map[string]forkGitHubAsset)
	for _, a := range r.Assets {
		if a.Name == "" || assets[a.Name].Name != "" || strings.ContainsAny(a.Name, "/\\") || a.URL != base+url.PathEscape(a.Name) || a.State != "uploaded" || a.Size <= 0 {
			return nil, errors.New("release contains an invalid, duplicate or foreign asset")
		}
		assets[a.Name] = a
	}
	for _, name := range append(append([]string{}, forkReleaseFiles...), "SHA256SUMS") {
		if assets[name].Name == "" {
			return nil, fmt.Errorf("release %s is incomplete: missing %s", r.Tag, name)
		}
	}
	manifest := assets["SHA256SUMS"]
	if manifest.Size > 64<<10 {
		return nil, errors.New("release checksum manifest is too large")
	}
	b, err := forkGet(ctx, manifest.URL, 64<<10)
	if err != nil {
		return nil, err
	}
	manifestSum := sha256.Sum256(b)
	if int64(len(b)) != manifest.Size || manifest.Digest != "" && manifest.Digest != "sha256:"+hex.EncodeToString(manifestSum[:]) {
		return nil, errors.New("release checksum manifest differs from GitHub metadata")
	}
	sums, err := forkChecksums(b)
	if err != nil {
		return nil, err
	}
	out := &Release{Version: forkNormalizedTag(r.Tag), URL: r.URL, Notes: InLang(r.Body, lang), Assets: make(map[string]Asset)}
	for _, name := range forkReleaseFiles {
		a := assets[name]
		digest := sums[name]
		if digest == "" || a.Digest != "" && a.Digest != "sha256:"+digest {
			return nil, fmt.Errorf("release %s has a missing or conflicting checksum for %s", r.Tag, name)
		}
		out.Assets[name] = Asset{URL: a.URL, Size: a.Size, SHA256: digest}
	}
	return out, nil
}

func forkChecksums(b []byte) (map[string]string, error) {
	out := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(string(b)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		if len(line) < 67 || line[64:66] != "  " && line[64:66] != " *" {
			return nil, errors.New("invalid release checksum manifest")
		}
		digest, name := strings.ToLower(line[:64]), line[66:]
		if _, err := hex.DecodeString(digest); err != nil || strings.ContainsAny(name, "/\\ \t\r") || name == "" || name == "." || name == ".." || out[name] != "" {
			return nil, errors.New("invalid or duplicate release checksum entry")
		}
		out[name] = digest
	}
	return out, scanner.Err()
}

func forkNotes(ctx context.Context, lang string) ([]Note, error) {
	list, err := forkReleaseList(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Note, 0, len(list))
	for _, r := range list {
		out = append(out, Note{Version: forkNormalizedTag(r.Tag), Notes: InLang(r.Body, lang), URL: r.URL})
	}
	return out, nil
}

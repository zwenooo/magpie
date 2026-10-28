package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func forkFixture(tag string) (forkGitHubRelease, string) {
	r := forkGitHubRelease{Tag: tag, Body: "English changes\n<!-- lang:zh -->\n中文更新", URL: forkReleases + "/tag/" + tag, Published: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	var sums strings.Builder
	for _, name := range forkReleaseFiles {
		sum := sha256.Sum256([]byte(name))
		digest := hex.EncodeToString(sum[:])
		fmt.Fprintf(&sums, "%s  %s\n", digest, name)
		r.Assets = append(r.Assets, forkGitHubAsset{Name: name, URL: forkReleases + "/download/" + tag + "/" + name, Size: int64(len(name)), State: "uploaded", Digest: "sha256:" + digest})
	}
	manifestSum := sha256.Sum256([]byte(sums.String()))
	r.Assets = append(r.Assets, forkGitHubAsset{Name: "SHA256SUMS", URL: forkReleases + "/download/" + tag + "/SHA256SUMS", Size: int64(sums.Len()), State: "uploaded", Digest: "sha256:" + hex.EncodeToString(manifestSum[:])})
	return r, sums.String()
}

func forkMock(t *testing.T, response func(*http.Request) (int, string, http.Header)) {
	t.Helper()
	before := client
	client = &http.Client{Transport: forkTransport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "" {
			t.Fatal("updater sent an account credential")
		}
		status, body, headers := response(r)
		return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	t.Cleanup(func() { client = before })
}

func TestForkGitHubCheckNotesAndDownload(t *testing.T) {
	t.Setenv("MAGPIE_UPDATE_FEED", "")
	t.Setenv("MAGPIE_NOTES_FEED", "")
	stableFor(t, "")
	r, sums := forkFixture("va.0.1.1725-vb.0.0.10")
	old, _ := forkFixture("va.0.1.1725-vb.0.0.9")
	preview, _ := forkFixture("va.9.0.0-vb.0.0.1")
	preview.Preview = true
	var downloads int
	forkMock(t, func(req *http.Request) (int, string, http.Header) {
		switch req.URL.String() {
		case forkUpdateFeed + "?per_page=100&page=1":
			b, _ := json.Marshal([]forkGitHubRelease{old, preview, r})
			return 200, string(b), nil
		case forkReleases + "/download/" + r.Tag + "/SHA256SUMS":
			return 200, sums, nil
		case forkReleases + "/download/" + r.Tag + "/" + BinaryAsset():
			downloads++
			return 200, BinaryAsset(), http.Header{"Content-Length": {fmt.Sprint(len(BinaryAsset()))}}
		default:
			t.Fatalf("unexpected URL (must stay in own fork): %s", req.URL)
			return 500, "", nil
		}
	})
	ctx := context.Background()
	rel, err := LatestIn(ctx, "zh")
	if err != nil || rel.Version != "0.1.1725-vb.0.0.10" || rel.Notes != "中文更新" || downloads != 0 || len(rel.Assets) != 16 {
		t.Fatalf("fork latest = %+v, %v; downloads=%d", rel, err, downloads)
	}
	notes, err := NotesBetween(ctx, old.Tag[3:], r.Tag[3:], "en")
	if err != nil || len(notes) != 1 || notes[0].Version != rel.Version || notes[0].Notes != "English changes" {
		t.Fatalf("fork notes = %+v, %v", notes, err)
	}
	back, err := NotesBack(ctx, rel.Version, 10, "zh")
	if err != nil || len(back) != 2 || back[1].Version != "0.1.1725-vb.0.0.9" {
		t.Fatalf("fork historical notes = %+v, %v", back, err)
	}
	path := filepath.Join(t.TempDir(), "download.new")
	if err := download(ctx, rel.Assets[BinaryAsset()], path); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != BinaryAsset() || downloads != 1 {
		t.Fatalf("verified payload %q, %v, requests=%d", b, err, downloads)
	}
}

func TestForkGitHubRejectsUntrustedOrIncompleteReleases(t *testing.T) {
	for _, tc := range []string{"draft", "prerelease", "official-tag", "foreign-page", "foreign-asset", "missing-asset", "duplicate-asset", "empty-asset", "missing-hash", "duplicate-hash", "bad-hash", "conflicting-digest", "traversal", "manifest-size", "manifest-digest"} {
		t.Run(tc, func(t *testing.T) {
			r, sums := forkFixture("va.0.1.1725-vb.0.0.6")
			switch tc {
			case "draft":
				r.Draft = true
			case "prerelease":
				r.Preview = true
			case "official-tag":
				r.Tag = "v0.1.1726"
			case "foreign-page":
				r.URL = "https://github.com/yetone/magpie/releases/tag/" + r.Tag
			case "foreign-asset":
				r.Assets[0].URL = "https://github.com/yetone/magpie/releases/download/" + r.Tag + "/" + r.Assets[0].Name
			case "missing-asset":
				r.Assets = r.Assets[1:]
			case "duplicate-asset":
				r.Assets = append(r.Assets, r.Assets[0])
			case "empty-asset":
				r.Assets[0].Size = 0
			case "missing-hash":
				sums = strings.Join(strings.Split(sums, "\n")[1:], "\n")
			case "duplicate-hash":
				sums += strings.Split(sums, "\n")[0] + "\n"
			case "bad-hash":
				sums = "z" + sums[1:]
			case "conflicting-digest":
				r.Assets[0].Digest = "sha256:" + strings.Repeat("0", 64)
			case "traversal":
				sums += strings.Repeat("0", 64) + "  ../other\n"
			}
			// Keep manifest metadata consistent so malformed entries exercise the
			// parser, not just its outer size/digest check.
			for i := range r.Assets {
				if r.Assets[i].Name == "SHA256SUMS" {
					a := &r.Assets[i]
					digest := sha256.Sum256([]byte(sums))
					a.Size, a.Digest = int64(len(sums)), "sha256:"+hex.EncodeToString(digest[:])
					if tc == "manifest-size" {
						a.Size++
					}
					if tc == "manifest-digest" {
						a.Digest = "sha256:" + strings.Repeat("0", 64)
					}
				}
			}
			forkMock(t, func(req *http.Request) (int, string, http.Header) { return 200, sums, nil })
			if _, err := forkRelease(context.Background(), r, ""); err == nil {
				t.Fatal("invalid release was accepted")
			}
		})
	}
}

func TestForkGitHubStableSelection(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	old, _ := forkFixture("va.0.1.1725-vb.0.0.9")
	old.Published = time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	fresh, _ := forkFixture("va.0.1.1725-vb.0.0.10")
	fresh.Published = now.Add(-time.Hour)
	list := []forkGitHubRelease{old, fresh}
	if got := forkChoose(list, false, now); got == nil || got.Tag != fresh.Tag {
		t.Fatal("every-version did not choose newer numeric fork revision")
	}
	if got := forkChoose(list, true, now); got == nil || got.Tag != old.Tag {
		t.Fatal("Stable did not preserve Monday/three-day soak")
	}
	list[1].Body = "> **Urgent release:** important fix"
	if got := forkChoose(list, true, now); got == nil || got.Tag != fresh.Tag {
		t.Fatal("urgent release was held back")
	}
	list[1].Published = now.Add(time.Hour)
	if got := forkChoose(list, true, now); got == nil || got.Tag != old.Tag {
		t.Fatal("future release selected")
	}
	if forkChoose([]forkGitHubRelease{fresh}, true, now) != nil {
		t.Fatal("unsoaked release selected on Stable")
	}
}

func TestForkGitHubPaginationAndFailure(t *testing.T) {
	t.Setenv("MAGPIE_UPDATE_FEED", "")
	t.Setenv("MAGPIE_NOTES_FEED", "")
	var pages []string
	r, _ := forkFixture("va.0.1.1725-vb.0.0.6")
	forkMock(t, func(req *http.Request) (int, string, http.Header) {
		page := req.URL.Query().Get("page")
		pages = append(pages, page)
		if page == "1" {
			invalid := make([]forkGitHubRelease, 100)
			for i := range invalid {
				invalid[i].Draft = true
			}
			b, _ := json.Marshal(invalid)
			return 200, string(b), nil
		}
		b, _ := json.Marshal([]forkGitHubRelease{r})
		return 200, string(b), nil
	})
	list, err := forkReleaseList(context.Background())
	if err != nil || len(list) != 1 || !reflect.DeepEqual(pages, []string{"1", "2"}) {
		t.Fatalf("pagination=%v releases=%v err=%v", pages, list, err)
	}
	for _, status := range []int{403, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			forkMock(t, func(*http.Request) (int, string, http.Header) {
				return status, "{}", http.Header{"X-Ratelimit-Remaining": {"0"}}
			})
			if _, err := forkReleaseList(context.Background()); err == nil {
				t.Fatal("HTTP failure accepted as empty release list")
			}
		})
	}
}

func TestForkGitHubRedirectAndSizeBound(t *testing.T) {
	for _, address := range []string{"http://api.github.com/repos/zwenooo/magpie/releases", "https://api.github.com/repos/yetone/magpie/releases", "https://evil.example/releases"} {
		t.Run(address, func(t *testing.T) {
			forkMock(t, func(r *http.Request) (int, string, http.Header) {
				if r.URL.String() != forkUpdateFeed {
					t.Fatal("followed forbidden redirect")
				}
				return 302, "", http.Header{"Location": {address}}
			})
			if _, err := forkGet(context.Background(), forkUpdateFeed, 100); err == nil {
				t.Fatal("foreign redirect accepted")
			}
		})
	}
	for _, address := range []string{"https://github.com/yetone/magpie/releases/download/v1/SHA256SUMS", "https://evil.example/SHA256SUMS", "http://release-assets.githubusercontent.com/SHA256SUMS"} {
		t.Run(address, func(t *testing.T) {
			manifest := forkReleases + "/download/va.0.1.1725-vb.0.0.6/SHA256SUMS"
			forkMock(t, func(r *http.Request) (int, string, http.Header) {
				if r.URL.String() != manifest {
					t.Fatal("followed forbidden checksum redirect")
				}
				return 302, "", http.Header{"Location": {address}}
			})
			if _, err := forkGet(context.Background(), manifest, 100); err == nil {
				t.Fatal("foreign checksum redirect accepted")
			}
		})
	}
	forkMock(t, func(r *http.Request) (int, string, http.Header) { return 200, strings.Repeat("x", 101), nil })
	if _, err := forkGet(context.Background(), forkUpdateFeed, 100); err == nil {
		t.Fatal("oversized body accepted")
	}
	if u, _ := url.Parse(Feed()); u.Host != "api.github.com" {
		t.Fatal("default feed is not GitHub")
	}
}

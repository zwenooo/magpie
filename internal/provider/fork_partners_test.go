package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type forkPartnerTransport struct{ asked *atomic.Int32 }

func (f forkPartnerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	f.asked.Add(1)
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
		Body: io.NopCloser(strings.NewReader(`{"partners":[]}`)), Request: request}, nil
}

func TestForkPartnersSource(t *testing.T) {
	for _, test := range []struct{ override, want string }{
		{"", "off"}, {"off", "off"}, {"https://catalog.example/partners", "https://catalog.example/partners"},
	} {
		if got := forkPartnersSource(test.override); got != test.want {
			t.Fatalf("source %q = %q, want %q", test.override, got, test.want)
		}
	}
}

func TestForkPartnersDisabledKeepsConfiguredProvider(t *testing.T) {
	foldHome(t)
	t.Setenv("MAGPIE_PARTNERS", "")
	resetPartners()
	t.Cleanup(resetPartners)
	partner := Partner{PresetDef: PresetDef{
		ID: "cached-partner", Name: "Cached provider", Kind: KindPartner,
		Chat: "https://provider.example/v1", KeysURL: "https://provider.example/keys",
		Regions: []Region{{ID: "eu", Name: "Europe", Chat: "https://eu.provider.example/v1"}},
	}, Notes: map[string]string{"en": "A paid promotion"}, IconURL: "https://provider.example/logo.png"}
	cache, err := json.Marshal(partnerCache{At: time.Now(), Partners: []Partner{partner}, Seen: map[string]Partner{partner.ID: partner}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(partnerCacheFile()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(partnerCacheFile(), cache, 0o600); err != nil {
		t.Fatal(err)
	}
	configured := Provider{ID: "my-provider", Name: "My provider", Preset: partner.ID,
		Key: "fixture-key", Chat: "https://eu.provider.example/v1", Models: []string{"fixture-model"}}
	saveAll(t, configured)
	providerBytes, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	var icons, requests atomic.Int32
	wasClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: forkPartnerTransport{asked: &requests}}
	t.Cleanup(func() { http.DefaultClient = wasClient })
	wasIcon := fetchPartnerIcon
	fetchPartnerIcon = func(context.Context, string) (string, error) { icons.Add(1); return "generic", nil }
	t.Cleanup(func() { fetchPartnerIcon = wasIcon })
	if got := Partners(); len(got) != 0 {
		t.Fatalf("cached promotions still shown: %v", partnerIDs(got))
	}
	if got := PartnersNow(time.Second); len(got) != 0 {
		t.Fatalf("CLI promotions still shown: %v", partnerIDs(got))
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); KeepPartnersFresh(ctx) }()
	select {
	case <-done:
		cancel()
	case <-time.After(5 * time.Second):
		cancel()
		<-done
		t.Fatal("disabled partner feed kept its background refresh loop running")
	}
	pr := Preset(partner.ID)
	if pr == nil || pr.Kind != KindRelay || pr.Sponsored || pr.Note != "" || pr.Chat != partner.Chat || pr.KeysURL != partner.KeysURL || !reflect.DeepEqual(pr.Regions, partner.Regions) {
		t.Fatalf("configured provider lost metadata or retained promotion: %+v", pr)
	}
	if got := All(); len(got) != 1 || got[0].ID != configured.ID || got[0].Key != configured.Key || got[0].Chat != configured.Chat || !reflect.DeepEqual(got[0].Models, configured.Models) {
		t.Fatal("existing provider configuration changed")
	}
	NoticePartners(partner.ID)
	if icons.Load() != 0 {
		t.Fatal("disabled promotions downloaded an icon")
	}
	if requests.Load() != 0 {
		t.Fatal("disabled promotions fetched a remote feed")
	}
	if got, err := os.ReadFile(partnerCacheFile()); err != nil || !bytes.Equal(got, cache) {
		t.Fatalf("disabled feed rewrote its cache: %v", err)
	}
	if got, err := os.ReadFile(Path()); err != nil || !bytes.Equal(got, providerBytes) {
		t.Fatalf("reading the old preset rewrote configured providers: %v", err)
	}
}

func TestForkPartnersDisabledKeepsOldCountsUnsent(t *testing.T) {
	foldHome(t)
	t.Setenv("MAGPIE_PARTNERS", "")
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("MAGPIE_NO_STATS", "")
	now := time.Now()
	day := now.Add(-24 * time.Hour).UTC().Format("2006-01-02")
	writePartnerCounts(partnerCounts{Days: map[string]map[string]map[string]int{day: {"cached-partner": {PartnerShown: 2}}}})
	before, err := os.ReadFile(partnerCountFile())
	if err != nil {
		t.Fatal(err)
	}
	if got := PartnerCounts(now); len(got) != 0 {
		t.Fatalf("disabled feed still exports old partner events: %v", got)
	}
	CountPartner(PartnerShown, "cached-partner")
	PartnerCountsSent(now)
	if got, err := os.ReadFile(partnerCountFile()); err != nil || !bytes.Equal(got, before) {
		t.Fatalf("disabled partner events changed stored counts: %v", err)
	}
}

func TestForkPartnersExplicitFeedStillWorks(t *testing.T) {
	foldHome(t)
	resetPartners()
	t.Cleanup(resetPartners)
	var asked atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		asked.Add(1)
		_, _ = w.Write([]byte(`{"partners":[{"id":"explicit-partner","name":"Explicit provider","chat":"https://provider.example/v1"}]}`))
	}))
	t.Cleanup(server.Close)
	t.Setenv("MAGPIE_PARTNERS", server.URL)
	got := PartnersNow(5 * time.Second)
	if len(got) != 1 || got[0].ID != "explicit-partner" || !got[0].Sponsored || asked.Load() != 1 {
		t.Fatalf("explicit local feed = %v; requests = %d", partnerIDs(got), asked.Load())
	}
}

package eepromupdater

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestFindLatest_PicksLexicallyGreatestPieeprom verifies we sort by name
// descending and pick the newest pieeprom-*.bin, while ignoring
// non-matching entries.
func TestFindLatest_PicksLexicallyGreatestPieeprom(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Sanity: we hit the GitHub Contents API for the expected dir.
		if !strings.Contains(r.URL.Path, "firmware-2712/stable") {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		entries := []map[string]any{
			{"name": "pieeprom-2024-01-01.bin", "type": "file", "size": 524288, "download_url": "https://example/pieeprom-2024-01-01.bin"},
			{"name": "pieeprom-2024-12-04.bin", "type": "file", "size": 2097152, "download_url": "https://example/pieeprom-2024-12-04.bin"},
			{"name": "pieeprom-2023-06-15.bin", "type": "file", "size": 524288, "download_url": "https://example/pieeprom-2023-06-15.bin"},
			{"name": "recovery.bin", "type": "file", "size": 1024, "download_url": "https://example/recovery.bin"},
			{"name": "subdir", "type": "dir", "size": 0, "download_url": ""},
		}
		_ = json.NewEncoder(w).Encode(entries)
	}))
	defer srv.Close()

	// Inject a transport that redirects api.github.com → our test server.
	client := &http.Client{
		Transport: rewriteTransport{to: srv.URL},
	}

	img, err := FindLatest(context.Background(), FindLatestOptions{
		Platform:   PlatformRPi5,
		Channel:    ChannelStable,
		HTTPClient: client,
	})
	if err != nil {
		t.Fatalf("FindLatest: %v", err)
	}
	if img.Name != "pieeprom-2024-12-04.bin" {
		t.Errorf("Name = %q; want pieeprom-2024-12-04.bin", img.Name)
	}
	if img.Version != "2024-12-04" {
		t.Errorf("Version = %q; want 2024-12-04", img.Version)
	}
	if img.Size != 2097152 {
		t.Errorf("Size = %d; want 2097152", img.Size)
	}
	if img.Platform != PlatformRPi5 {
		t.Errorf("Platform = %q; want %q", img.Platform, PlatformRPi5)
	}
}

func TestFindLatest_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
	}))
	defer srv.Close()
	client := &http.Client{Transport: rewriteTransport{to: srv.URL}}

	_, err := FindLatest(context.Background(), FindLatestOptions{HTTPClient: client})
	if err == nil {
		t.Fatal("expected error on 404")
	}
}

func TestFindLatest_NoBinariesInDirectory(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"name": "README.md", "type": "file", "size": 100, "download_url": "https://example/README.md"},
		})
	}))
	defer srv.Close()
	client := &http.Client{Transport: rewriteTransport{to: srv.URL}}

	_, err := FindLatest(context.Background(), FindLatestOptions{HTTPClient: client})
	if err == nil {
		t.Fatal("expected error when no pieeprom-*.bin entries are present")
	}
}

func TestDownload_VerifiesSize(t *testing.T) {
	body := []byte("not-really-an-eeprom-image")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	img := &Image{Name: "pieeprom-test.bin", URL: srv.URL, Size: int64(len(body))}
	got, err := Download(context.Background(), img, srv.Client())
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("body mismatch: got %q want %q", got, body)
	}
}

func TestDownload_RejectsSizeMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("short"))
	}))
	defer srv.Close()
	img := &Image{Name: "pieeprom-test.bin", URL: srv.URL, Size: 1000}
	if _, err := Download(context.Background(), img, srv.Client()); err == nil {
		t.Fatal("expected size-mismatch error")
	}
}

// rewriteTransport sends every request to `to` regardless of its hostname.
// Lets the test pin the GitHub API call to a httptest server without
// patching the production URL.
type rewriteTransport struct{ to string }

func (rt rewriteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	// Preserve the path + query; swap scheme/host.
	prefix, _, _ := strings.Cut(strings.TrimPrefix(rt.to, "http://"), "/")
	url := fmt.Sprintf("http://%s%s", prefix, r.URL.RequestURI())
	req, err := http.NewRequestWithContext(r.Context(), r.Method, url, r.Body)
	if err != nil {
		return nil, err
	}
	req.Header = r.Header
	return http.DefaultTransport.RoundTrip(req)
}

// Silence unused-import warnings if io/test changes shape later.
var _ = io.ReadAll

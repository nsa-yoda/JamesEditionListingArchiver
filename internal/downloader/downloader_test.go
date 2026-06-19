package downloader

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var tinyPNG = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 600)...)

func TestFetchPageRedirectCookieAndRetry(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start":
			http.SetCookie(w, &http.Cookie{Name: "allowed", Value: "yes", Path: "/"})
			http.Redirect(w, r, "/page", http.StatusFound)
		case "/page":
			if cookie, err := r.Cookie("allowed"); err != nil || cookie.Value != "yes" {
				http.Error(w, "missing cookie", http.StatusForbidden)
				return
			}
			if attempts.Add(1) < 2 {
				http.Error(w, "retry", http.StatusServiceUnavailable)
				return
			}
			fmt.Fprint(w, "<html>ok</html>")
		}
	}))
	defer server.Close()
	client, err := NewClient(ClientConfig{Timeout: time.Second, UserAgent: "test"})
	if err != nil {
		t.Fatal(err)
	}
	body, finalURL, err := client.FetchPage(context.Background(), server.URL+"/start")
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "<html>ok</html>" || finalURL != server.URL+"/page" {
		t.Fatalf("got %q %q", body, finalURL)
	}
}

func TestFetchPageExplainsForbiddenWithoutResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "private challenge details", http.StatusForbidden)
	}))
	defer server.Close()
	client, _ := NewClient(ClientConfig{Timeout: time.Second, UserAgent: "test"})
	client.SkippedCookies = 1
	_, _, err := client.FetchPage(context.Background(), server.URL)
	if err == nil {
		t.Fatal("expected forbidden error")
	}
	if !strings.Contains(err.Error(), "1 cookie record(s) could not be safely scoped or serialized and were skipped") || !strings.Contains(err.Error(), "will not bypass access controls") || strings.Contains(err.Error(), "private challenge details") {
		t.Fatalf("error = %q", err)
	}
}

func TestFetchPageIdentifiesCloudflareChallenge(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("CF-Mitigated", "challenge")
		http.Error(w, "challenge body", http.StatusForbidden)
	}))
	defer server.Close()
	client, _ := NewClient(ClientConfig{Timeout: time.Second, UserAgent: "test"})
	_, _, err := client.FetchPage(context.Background(), server.URL)
	if err == nil {
		t.Fatal("expected challenge error")
	}
	if !strings.Contains(err.Error(), "Cloudflare returned a browser challenge") || strings.Contains(err.Error(), "challenge body") {
		t.Fatalf("error = %q", err)
	}
}

func TestDownloadImagesPartialFailureResumeAndIdempotency(t *testing.T) {
	var imageRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bad" {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<html>not image</html>")
			return
		}
		imageRequests.Add(1)
		w.Header().Set("Content-Type", "image/png")
		if r.Header.Get("Range") != "" {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 10-%d/%d", len(tinyPNG)-1, len(tinyPNG)))
			w.WriteHeader(http.StatusPartialContent)
			w.Write(tinyPNG[10:])
			return
		}
		w.Write(tinyPNG)
	}))
	defer server.Close()
	client, _ := NewClient(ClientConfig{Timeout: time.Second, UserAgent: "test"})
	dir := t.TempDir()
	urls := []string{server.URL + "/image", server.URL + "/bad"}
	hashPart := filepath.Join(dir, "001-")
	results := client.DownloadImages(context.Background(), urls, dir, ImageOptions{Workers: 2})
	if len(results) != 2 || results[0].File == "" || results[1].Error == "" {
		t.Fatalf("unexpected results: %#v", results)
	}
	firstRequests := imageRequests.Load()
	results = client.DownloadImages(context.Background(), urls[:1], dir, ImageOptions{Workers: 2})
	if imageRequests.Load() != firstRequests || results[0].File == "" {
		t.Fatalf("idempotent rerun downloaded again: %#v", results)
	}

	os.Remove(filepath.Join(dir, results[0].File))
	matches, _ := filepath.Glob(hashPart + "*")
	for _, match := range matches {
		os.Remove(match)
	}
	// The deterministic hash is intentionally opaque; create a partial from a
	// first interrupted-shaped run by renaming after deriving the base.
	results = client.DownloadImages(context.Background(), urls[:1], dir, ImageOptions{Workers: 1})
	full := filepath.Join(dir, results[0].File)
	data, _ := os.ReadFile(full)
	part := full[:len(full)-len(filepath.Ext(full))] + ".part"
	os.WriteFile(part, data[:10], 0o644)
	os.Remove(full)
	results = client.DownloadImages(context.Background(), urls[:1], dir, ImageOptions{Workers: 1})
	if results[0].Error != "" {
		t.Fatal(results[0].Error)
	}
	got, _ := os.ReadFile(filepath.Join(dir, results[0].File))
	if !bytes.Equal(got, tinyPNG) {
		t.Fatalf("resume produced %d bytes, want %d", len(got), len(tinyPNG))
	}
}

func TestDownloadImageRejectsMismatchedResumeRange(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", len(tinyPNG)-1, len(tinyPNG)))
		w.WriteHeader(http.StatusPartialContent)
		w.Write(tinyPNG)
	}))
	defer server.Close()
	client, _ := NewClient(ClientConfig{Timeout: time.Second, UserAgent: "test"})
	dir := t.TempDir()
	rawURL := server.URL + "/image"
	hash := sha256.Sum256([]byte(rawURL))
	part := filepath.Join(dir, fmt.Sprintf("img-%x.part", hash[:6]))
	if err := os.WriteFile(part, tinyPNG[:10], 0o644); err != nil {
		t.Fatal(err)
	}
	results := client.DownloadImages(context.Background(), []string{rawURL}, dir, ImageOptions{Workers: 1})
	if len(results) != 1 || results[0].Error != "invalid Content-Range for resumed image" {
		t.Fatalf("results = %#v", results)
	}
	if _, err := os.Stat(part); !os.IsNotExist(err) {
		t.Fatalf("partial file was not removed: %v", err)
	}
}

func TestSafeAndDisplayURL(t *testing.T) {
	rawURL := "https://user:secret@example.test/listing?token=private#section"
	if err := SafeURL(rawURL); err == nil {
		t.Fatal("expected embedded credential error")
	}
	if got := DisplayURL(rawURL); got != "https://example.test/listing" {
		t.Fatalf("DisplayURL() = %q", got)
	}
}

func TestDownloadImageRedactsRefererQuery(t *testing.T) {
	var referer string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		referer = r.Header.Get("Referer")
		w.Header().Set("Content-Type", "image/png")
		w.Write(tinyPNG)
	}))
	defer server.Close()
	client, _ := NewClient(ClientConfig{Timeout: time.Second, UserAgent: "test"})
	results := client.DownloadImages(context.Background(), []string{server.URL + "/image"}, t.TempDir(), ImageOptions{
		Workers: 1, Referer: "https://example.test/listing?token=private",
	})
	if len(results) != 1 || results[0].Error != "" {
		t.Fatalf("results = %#v", results)
	}
	if referer != "https://example.test/listing" {
		t.Fatalf("Referer = %q", referer)
	}
}

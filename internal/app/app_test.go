package app

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunUsageErrorExitCode(t *testing.T) {
	var stderr bytes.Buffer
	if code := Run(context.Background(), nil, &bytes.Buffer{}, &stderr); code != 2 {
		t.Fatalf("exit code = %d", code)
	}
	if !strings.Contains(stderr.String(), "at least one URL, -input, -html, -html-dir, or -reindex is required") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunReindexOnly(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "Country", "Region"), 0o755); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"-reindex", "-root", root}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Rebuilt browser indexes in 3 directories") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(root, "Country", "Region", "index.html")); err != nil {
		t.Fatal(err)
	}
}

func TestRunHelpExitCode(t *testing.T) {
	var stderr bytes.Buffer
	if code := Run(context.Background(), []string{"-h"}, &bytes.Buffer{}, &stderr); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if !strings.Contains(stderr.String(), "Usage of listing-archiver") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunBatchContinuesAfterFailures(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{
		"-url", "file:///tmp/one?token=private-one",
		"-url", "file:///tmp/two?token=private-two",
	}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d", code)
	}
	if strings.Count(stderr.String(), "unsupported URL scheme") != 2 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), "Completed 0/2 listings; 2 failed.") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if strings.Contains(stderr.String(), "private-") {
		t.Fatalf("stderr exposed URL query: %q", stderr.String())
	}
}

func TestRunWithInputReadsStdin(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := RunWithInput(context.Background(), []string{"-input", "-"}, strings.NewReader("file:///tmp/one\nfile:///tmp/two\n"), &stdout, &stderr)
	if code != 1 || !strings.Contains(stdout.String(), "Completed 0/2 listings; 2 failed.") {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
}

func TestRunRedactsQueryFromUnsupportedSiteError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "<html></html>")
	}))
	defer server.Close()
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"-url", server.URL + "/listing?token=private"}, &bytes.Buffer{}, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d", code)
	}
	if strings.Contains(stderr.String(), "private") {
		t.Fatalf("stderr exposed query: %q", stderr.String())
	}
}

func TestRunOperationalErrorExitCode(t *testing.T) {
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"-url", "file:///tmp/no"}, &bytes.Buffer{}, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d", code)
	}
	if !strings.Contains(stderr.String(), "unsupported URL scheme") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunHTMLImport(t *testing.T) {
	fixture := filepath.Join("..", "site", "jamesedition", "testdata", "listing.html")
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{
		"-html", fixture,
		"-source-url", "https://www.jamesedition.com/real_estate/example-nj-usa/10-main-st-12345",
		"-metadata-only",
		"-cookies", filepath.Join(t.TempDir(), "missing-cookies.json"),
		"-root", root,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Imported HTML archive to:") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunHTMLDirectoryContinuesAfterMissingSource(t *testing.T) {
	dir := t.TempDir()
	fixture, err := os.ReadFile(filepath.Join("..", "site", "jamesedition", "testdata", "listing.html"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "valid.html"), fixture, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "invalid.html"), []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"-html-dir", dir, "-metadata-only", "-root", t.TempDir()}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(stdout.String(), "Imported 1/2 HTML files; 0 skipped; 1 failed.") || !strings.Contains(stderr.String(), "no usable source URL") {
		t.Fatalf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
	}
}

func TestRunHTMLDirectorySkipsExistingUnlessRefresh(t *testing.T) {
	dir := t.TempDir()
	fixture, err := os.ReadFile(filepath.Join("..", "site", "jamesedition", "testdata", "listing.html"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "valid.html"), fixture, 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	var firstOut, firstErr bytes.Buffer
	if code := Run(context.Background(), []string{"-html-dir", dir, "-metadata-only", "-root", root}, &firstOut, &firstErr); code != 0 {
		t.Fatalf("first code = %d, stderr = %q", code, firstErr.String())
	}

	var skipOut, skipErr bytes.Buffer
	if code := Run(context.Background(), []string{"-html-dir", dir, "-metadata-only", "-root", root}, &skipOut, &skipErr); code != 0 {
		t.Fatalf("skip code = %d, stderr = %q", code, skipErr.String())
	}
	if !strings.Contains(skipOut.String(), "Skipped existing: ") || !strings.Contains(skipOut.String(), "[found: /") || strings.Contains(skipOut.String(), "Imported HTML archive to:") {
		t.Fatalf("skip stdout = %q", skipOut.String())
	}

	var refreshOut, refreshErr bytes.Buffer
	if code := Run(context.Background(), []string{"-html-dir", dir, "-metadata-only", "-refresh", "-root", root}, &refreshOut, &refreshErr); code != 0 {
		t.Fatalf("refresh code = %d, stderr = %q", code, refreshErr.String())
	}
	if strings.Contains(refreshOut.String(), "Skipped existing:") || !strings.Contains(refreshOut.String(), "Imported HTML archive to:") {
		t.Fatalf("refresh stdout = %q", refreshOut.String())
	}
}

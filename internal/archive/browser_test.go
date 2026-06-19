package archive

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestUpdateBrowserIndexes(t *testing.T) {
	root := t.TempDir()
	leaf := filepath.Join(root, "United States", "New York")
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leaf, "listing data.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leaf, "index.json"), []byte(`{"legacy":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(leaf, "Albany"), 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 10, 13, 14, 23, 0, time.UTC)
	if err := updateBrowserIndexes(root, leaf, now); err != nil {
		t.Fatal(err)
	}

	rootIndex := readBrowserIndex(t, root)
	if rootIndex.Path != "." || rootIndex.Parent != "" {
		t.Fatalf("root index = %#v", rootIndex)
	}
	if !reflect.DeepEqual(rootIndex.Directories, []browserDirectory{{Name: "United States", Href: "United%20States/index.html"}}) {
		t.Fatalf("root directories = %#v", rootIndex.Directories)
	}

	leafIndex := readBrowserIndex(t, leaf)
	if leafIndex.Path != "United States/New York" || leafIndex.Parent != "../index.html" || !leafIndex.LastUpdated.Equal(now) {
		t.Fatalf("leaf index = %#v", leafIndex)
	}
	if !reflect.DeepEqual(leafIndex.Directories, []browserDirectory{{Name: "Albany", Href: "Albany/index.html"}}) {
		t.Fatalf("leaf directories = %#v", leafIndex.Directories)
	}
	if !reflect.DeepEqual(leafIndex.Files, []browserFile{{Name: "listing data.json", Href: "listing%20data.json", Size: 2}}) {
		t.Fatalf("leaf files = %#v", leafIndex.Files)
	}
	jsonIndex := readBrowserJSONIndex(t, leaf)
	if !reflect.DeepEqual(jsonIndex, leafIndex) {
		t.Fatalf("index.json and index.js differ: JSON=%#v JS=%#v", jsonIndex, leafIndex)
	}
}

func TestBrowserIndexSkipsSymlinks(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(target, []byte("private"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "outside")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := updateBrowserIndexes(root, root, time.Now()); err != nil {
		t.Fatal(err)
	}
	index := readBrowserIndex(t, root)
	if len(index.Files) != 0 || len(index.Directories) != 0 {
		t.Fatalf("symlink was indexed: %#v", index)
	}
}

func TestBrowserPageUsesJSONWithJavaScriptFallback(t *testing.T) {
	page, err := browserAssets.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	source := string(page)
	for _, expected := range []string{`href="../index.html"`, `join(" > ")`, `fetch("./index.json"`, `script.src = "./index.js"`, `window.listingArchiveIndex`, `fetch("./listing.json"`, `script.src = "./listing.js"`, `window.listingArchiveListing`, `className = "media-gallery"`, `gap: 30px`, `width: 100vw`, `video-js.min.css`, `video.min.js`, `videojs(video`, `object-fit: contain`, `listing-toolbar`, `Copy URL`, `data-filter-button`} {
		if !strings.Contains(source, expected) {
			t.Fatalf("browser page does not contain %q", expected)
		}
	}
	if strings.Contains(source, `<script src="./index.js">`) {
		t.Fatal("browser page loads index.js before trying index.json")
	}
}

func TestUpdateBrowserIndexesRejectsEscapingLeaf(t *testing.T) {
	if err := updateBrowserIndexes(t.TempDir(), t.TempDir(), time.Now()); err == nil {
		t.Fatal("expected escaping leaf error")
	}
}

func TestRebuildBrowserIndexesIncludesExistingBranches(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{
		filepath.Join(root, "United States", "New York"),
		filepath.Join(root, "France", "Paris"),
	} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	listingDir := filepath.Join(root, "United States", "New York")
	if err := os.WriteFile(filepath.Join(listingDir, "listing.json"), []byte(`{"schema_version":1,"property":{"title":"Existing listing"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	count, err := RebuildBrowserIndexes(root, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if count != 5 {
		t.Fatalf("indexed %d directories, want 5", count)
	}
	for _, directory := range []string{root, filepath.Join(root, "United States", "New York"), filepath.Join(root, "France", "Paris")} {
		if _, err := os.Stat(filepath.Join(directory, "index.html")); err != nil {
			t.Fatalf("%s was not indexed: %v", directory, err)
		}
		if _, err := os.Stat(filepath.Join(directory, "index.js")); err != nil {
			t.Fatalf("%s index data was not generated: %v", directory, err)
		}
		if _, err := os.Stat(filepath.Join(directory, "index.json")); err != nil {
			t.Fatalf("%s JSON index data was not generated: %v", directory, err)
		}
	}
	listingScript, err := os.ReadFile(filepath.Join(listingDir, "listing.js"))
	if err != nil {
		t.Fatalf("listing.js was not rebuilt: %v", err)
	}
	if !strings.Contains(string(listingScript), `"title":"Existing listing"`) {
		t.Fatalf("unexpected listing.js: %s", listingScript)
	}
	for _, asset := range []string{"video.min.js", "video-js.min.css"} {
		if _, err := os.Stat(filepath.Join(listingDir, asset)); err != nil {
			t.Fatalf("%s browser asset was not generated: %v", asset, err)
		}
	}
}

func readBrowserJSONIndex(t *testing.T, directory string) browserIndex {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(directory, "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	var index browserIndex
	if err := json.Unmarshal(data, &index); err != nil {
		t.Fatal(err)
	}
	return index
}

func readBrowserIndex(t *testing.T, directory string) browserIndex {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(directory, "index.js"))
	if err != nil {
		t.Fatal(err)
	}
	prefix := []byte("window." + browserIndexVariable + " = ")
	if !strings.HasPrefix(string(data), string(prefix)) || !strings.HasSuffix(string(data), ";\n") {
		t.Fatalf("invalid browser index script: %q", data)
	}
	data = data[len(prefix) : len(data)-2]
	var index browserIndex
	if err := json.Unmarshal(data, &index); err != nil {
		t.Fatal(err)
	}
	return index
}

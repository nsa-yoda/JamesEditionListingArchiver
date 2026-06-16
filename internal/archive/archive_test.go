package archive

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"listing-archiver/internal/downloader"
	"listing-archiver/internal/model"
	"listing-archiver/internal/site"
	"listing-archiver/internal/site/jamesedition"
)

func TestRunIdempotentWithPartialImageFailure(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("..", "site", "jamesedition", "testdata", "listing.html"))
	if err != nil {
		t.Fatal(err)
	}
	fixture = []byte(strings.ReplaceAll(string(fixture), "https://www.jamesedition.com/", "/"))
	fixture = []byte(strings.Replace(string(fixture), "</body>", `<img src="/images/second.jpg"></body>`, 1))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/real_estate/example-nj-usa/10-main-st-12345":
			w.Header().Set("Content-Type", "text/html")
			w.Write(fixture)
		case "/images/original.jpg", "/images/second.jpg":
			w.Header().Set("Content-Type", "image/png")
			w.Write(append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 600)...))
		default:
			http.Error(w, "missing", http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, _ := downloader.NewClient(downloader.ClientConfig{Timeout: time.Second, UserAgent: "test"})
	extractor := jamesedition.Extractor{Now: func() time.Time { return time.Unix(1, 0) }}
	// The adapter deliberately matches JamesEdition. Wrap the local server URL
	// in a test-only matcher while retaining its extraction behavior.
	service := Service{Client: client, Extractors: []site.Extractor{localExtractor{Extractor: extractor}}}
	rawURL := server.URL + "/real_estate/example-nj-usa/10-main-st-12345"
	options := Options{Root: t.TempDir(), Workers: 2}
	first, err := service.Run(context.Background(), rawURL, options)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Run(context.Background(), rawURL, options)
	if err != nil {
		t.Fatal(err)
	}
	if first.Directory != second.Directory || len(second.Listing.Images) != 2 {
		t.Fatalf("unexpected rerun: %#v", second)
	}
	data, err := os.ReadFile(filepath.Join(second.Directory, "listing.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest model.Listing
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != model.SchemaVersion {
		t.Fatalf("schema version = %d", manifest.SchemaVersion)
	}
	assertBrowserIndexHierarchy(t, options.Root, second.Directory)

	metadataOnly, err := service.Run(context.Background(), rawURL, Options{
		Root: t.TempDir(), Workers: 2, MetadataOnly: true, MaxImages: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if metadataOnly.Discovered != 2 || len(metadataOnly.Listing.Images) != 1 || metadataOnly.Listing.Images[0].File != "" {
		t.Fatalf("unexpected metadata-only result: %#v", metadataOnly)
	}
	if len(metadataOnly.Listing.Warnings) != 1 {
		t.Fatalf("warnings = %#v", metadataOnly.Listing.Warnings)
	}
}

func TestImportHTMLPreservesSourceAndSkipsPageFetch(t *testing.T) {
	html, err := os.ReadFile(filepath.Join("..", "site", "jamesedition", "testdata", "listing.html"))
	if err != nil {
		t.Fatal(err)
	}
	service := Service{Extractors: []site.Extractor{jamesedition.Extractor{Now: func() time.Time { return time.Unix(1, 0) }}}}
	sourceURL := "https://www.jamesedition.com/real_estate/example-nj-usa/10-main-st-12345"
	result, err := service.ImportHTML(context.Background(), sourceURL, html, Options{
		Root: t.TempDir(), Workers: 1, MetadataOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(filepath.Join(result.Directory, "source.html"))
	if err != nil {
		t.Fatal(err)
	}
	if string(saved) != string(html) || result.Listing.Source.URL != sourceURL || len(result.Listing.Images) == 0 {
		t.Fatalf("unexpected import result: %#v", result)
	}
	listingScript, err := os.ReadFile(filepath.Join(result.Directory, "listing.js"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(listingScript), "window.listingArchiveListing = {") {
		t.Fatalf("unexpected listing.js: %s", listingScript)
	}
}

func TestImportHTMLRejectsChallengePage(t *testing.T) {
	service := Service{Extractors: []site.Extractor{jamesedition.Extractor{}}}
	_, err := service.ImportHTML(context.Background(), "https://www.jamesedition.com/real_estate/example", []byte(`<title>Just a moment...</title><script src="/cf-chl-x"></script>`), Options{MetadataOnly: true})
	if err == nil || !strings.Contains(err.Error(), "Cloudflare challenge page") {
		t.Fatalf("error = %v", err)
	}
}

func TestImportHTMLDownloadsImagesWithoutFetchingPage(t *testing.T) {
	var pageRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/image.jpg" {
			w.Header().Set("Content-Type", "image/png")
			w.Write(append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 600)...))
			return
		}
		pageRequests.Add(1)
		http.Error(w, "page fetch not expected", http.StatusInternalServerError)
	}))
	defer server.Close()
	html := []byte(`<html><head><meta property="og:title" content="Imported listing"></head><body><img src="/image.jpg"></body></html>`)
	client, err := downloader.NewClient(downloader.ClientConfig{Timeout: time.Second, UserAgent: "test"})
	if err != nil {
		t.Fatal(err)
	}
	service := Service{Client: client, Extractors: []site.Extractor{localExtractor{Extractor: jamesedition.Extractor{}}}}
	result, err := service.ImportHTML(context.Background(), server.URL+"/listing", html, Options{Root: t.TempDir(), Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	if pageRequests.Load() != 0 || len(result.Listing.Images) != 1 || result.Listing.Images[0].File == "" {
		t.Fatalf("unexpected import result: page requests=%d, images=%#v", pageRequests.Load(), result.Listing.Images)
	}
}

func assertBrowserIndexHierarchy(t *testing.T, root, listingDirectory string) {
	t.Helper()
	source, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	imagesDirectory := filepath.Join(listingDirectory, "images")
	rel, err := filepath.Rel(root, imagesDirectory)
	if err != nil {
		t.Fatal(err)
	}
	directories := []string{root}
	current := root
	for _, component := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, component)
		directories = append(directories, current)
	}
	for _, directory := range directories {
		page, err := os.ReadFile(filepath.Join(directory, "index.html"))
		if err != nil {
			t.Fatalf("read %s/index.html: %v", directory, err)
		}
		if string(page) != string(source) {
			t.Fatalf("%s/index.html differs from source", directory)
		}
		data, err := os.ReadFile(filepath.Join(directory, "index.js"))
		if err != nil {
			t.Fatalf("read %s/index.js: %v", directory, err)
		}
		prefix := []byte("window." + browserIndexVariable + " = ")
		data = data[len(prefix) : len(data)-2]
		var index browserIndex
		if err := json.Unmarshal(data, &index); err != nil {
			t.Fatalf("parse %s/index.js: %v", directory, err)
		}
		if index.SchemaVersion != browserIndexSchemaVersion || index.LastUpdated.IsZero() {
			t.Fatalf("invalid browser index at %s: %#v", directory, index)
		}
		jsonData, err := os.ReadFile(filepath.Join(directory, "index.json"))
		if err != nil {
			t.Fatalf("read %s/index.json: %v", directory, err)
		}
		var jsonIndex browserIndex
		if err := json.Unmarshal(jsonData, &jsonIndex); err != nil {
			t.Fatalf("parse %s/index.json: %v", directory, err)
		}
		if jsonIndex.Path != index.Path || !jsonIndex.LastUpdated.Equal(index.LastUpdated) {
			t.Fatalf("browser index formats differ at %s", directory)
		}
	}
}

type localExtractor struct{ jamesedition.Extractor }

func (localExtractor) Match(string) bool { return true }

func TestWriteAtomic(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "value")
	if err := writeAtomic(filename, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(filename, []byte("second"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filename)
	if string(got) != "second" {
		t.Fatalf("got %q", got)
	}
}

func TestRenderMarkdownIncludesMapURL(t *testing.T) {
	listing := model.New("https://example.test/listing", "test", time.Unix(1, 0))
	listing.Property.Title = "Mapped listing"
	listing.Location.MapURL = "https://www.google.com/maps/search/?api=1&query=40.1,-74.2"
	markdown := renderMarkdown(listing)
	if !strings.Contains(markdown, "- Map: "+listing.Location.MapURL) {
		t.Fatalf("markdown does not contain map URL:\n%s", markdown)
	}
}

func TestRenderMarkdownIncludesPreservedListingDetails(t *testing.T) {
	listing := model.New("https://example.test/listing", "test", time.Unix(1, 0))
	listing.Property.Title = "Detailed listing"
	listing.Source.ListingReference = "REF-9"
	listing.Property.InteriorArea = &model.Measurement{Display: "7,054 Sqft"}
	listing.Property.PricePerArea = &model.UnitPrice{Display: "$637"}
	listing.Broker.Agent = "Example Agent"
	listing.Broker.Agency = "Example Agency"
	markdown := renderMarkdown(listing)
	for _, expected := range []string{"- Listing reference: REF-9", "- Interior area: 7,054 Sqft", "- Price per area: $637", "- Agent: Example Agent", "- Agency: Example Agency"} {
		if !strings.Contains(markdown, expected) {
			t.Fatalf("markdown does not contain %q:\n%s", expected, markdown)
		}
	}
}

func TestEnsureSameArchiveRejectsCollision(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "listing.json"), []byte(`{"source":{"url":"https://one","canonical_url":"https://one"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	listing := model.New("https://two", "test", time.Now())
	listing.Source.CanonicalURL = "https://two"
	if err := ensureSameArchive(dir, listing); err == nil {
		t.Fatal("expected collision error")
	}
}

func TestBuildExistingIndexFindsManifestByURLAndListingID(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Canada", "Ontario", "Oakville", "Old Title Path")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	listing := model.New("https://www.jamesedition.com/real_estate/oakville-canada/old-title-16017322", "jamesedition", time.Now())
	listing.Source.CanonicalURL = listing.Source.URL
	listing.Source.ListingID = "16017322"
	data, err := json.Marshal(listing)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "listing.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	index, err := BuildExistingIndex(root)
	if err != nil {
		t.Fatal(err)
	}
	incoming := model.New("https://www.jamesedition.com/real_estate/oakville-canada/new-clean-address-16017322", "jamesedition", time.Now())
	incoming.Source.CanonicalURL = incoming.Source.URL
	incoming.Source.ListingID = "16017322"
	found, ok := index.Find(incoming)
	if !ok || found != dir {
		t.Fatalf("found = %q, %v", found, ok)
	}
}

func TestMoveExistingArchiveToAddressPath(t *testing.T) {
	root := t.TempDir()
	oldDir := filepath.Join(root, "Canada", "Ontario", "Oakville", "Old Marketing Title")
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	listing := model.New("https://www.jamesedition.com/real_estate/oakville-canada/listing-16017322", "jamesedition", time.Now())
	listing.Source.CanonicalURL = listing.Source.URL
	listing.Source.ListingID = "16017322"
	listing.Location.Country = "Canada"
	listing.Location.Region = "Ontario"
	listing.Location.Municipality = "Oakville"
	listing.Location.Street = "2054 Lakeshore Rd E"
	data, err := json.Marshal(listing)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "listing.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	newDir, moved, err := MoveExistingArchive(root, oldDir, listing)
	if err != nil {
		t.Fatal(err)
	}
	if !moved || !strings.HasSuffix(newDir, filepath.Join("Canada", "Ontario", "Oakville", "2054 Lakeshore Rd E")) {
		t.Fatalf("newDir = %q, moved = %v", newDir, moved)
	}
	if _, err := os.Stat(filepath.Join(newDir, "listing.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatalf("old directory still exists: %v", err)
	}
}

func TestEnsureSameArchiveRejectsUnrelatedNonEmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "unrelated"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	listing := model.New("https://example.test/listing", "test", time.Now())
	listing.Source.CanonicalURL = listing.Source.URL
	if err := ensureSameArchive(dir, listing); err == nil {
		t.Fatal("expected non-empty directory error")
	}
}

func TestResolveListingDirectoryUsesStableCollisionSuffix(t *testing.T) {
	root := t.TempDir()
	first := model.New("https://example.test/one", "test", time.Now())
	first.Source.CanonicalURL = first.Source.URL
	first.Location.Country = "US"
	first.Location.Region = "NJ"
	first.Location.Municipality = "Example"
	first.Location.Street = "10 Main St"
	firstDir, err := resolveListingDirectory(root, first)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(firstDir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(first)
	if err := os.WriteFile(filepath.Join(firstDir, "listing.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	second := first
	second.Source.URL = "https://example.test/two"
	second.Source.CanonicalURL = second.Source.URL
	second.Source.ListingID = "LIST-2"
	secondDir, err := resolveListingDirectory(root, second)
	if err != nil {
		t.Fatal(err)
	}
	if secondDir == firstDir || !strings.HasSuffix(secondDir, "10 Main St [LIST-2]") {
		t.Fatalf("collision directory = %q", secondDir)
	}
}

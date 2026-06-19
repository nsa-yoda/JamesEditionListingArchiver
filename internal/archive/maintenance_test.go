package archive

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"listing-archiver/internal/model"
)

func TestVerifyArchiveReportsStaleManifestAndIndex(t *testing.T) {
	root := t.TempDir()
	listingDir := filepath.Join(root, "Country", "Region", "Municipality", "10 Main St")
	if err := os.MkdirAll(filepath.Join(listingDir, "images"), 0o755); err != nil {
		t.Fatal(err)
	}
	listing := model.New("https://example.test/listing", "test", time.Unix(1, 0))
	listing.Source.CanonicalURL = listing.Source.URL
	listing.Location.Country = "Country"
	listing.Location.Region = "Region"
	listing.Location.Municipality = "Municipality"
	listing.Location.Street = "10 Main St"
	listing.Images = []model.Asset{{SourceURL: "https://example.test/one.jpg", File: "img-1.jpg", Status: "existing"}}
	data, _ := json.MarshalIndent(listing, "", "  ")
	if err := os.WriteFile(filepath.Join(listingDir, "listing.json"), append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(listingDir, "listing.js"), []byte("window.listingArchiveListing = {};\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(listingDir, "source.url"), []byte(listing.Source.URL+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(listingDir, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(listingDir, "source.html"), []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(listingDir, "images", "img-1.jpg"), []byte("not-an-image-but-still-a-file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(filepath.Join(listingDir, "index.html"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(filepath.Join(listingDir, "index.json"), []byte(`{"path":"stale"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(filepath.Join(listingDir, "index.js"), []byte("window.listingArchiveIndex = {};\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(listingDir, "manifest.json"), []byte(`{"schema_version":1,"files":[],"assets":[],"summary":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := VerifyArchive(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Issues) == 0 {
		t.Fatal("expected verification issues")
	}
	joined := strings.Join(report.Issues, "\n")
	for _, expected := range []string{"stale index.html", "stale index.json", "manifest.json file inventory is stale"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("verify issues do not contain %q:\n%s", expected, joined)
		}
	}
}

func TestMigrateArchiveUpdatesSchemaAndWritesManifest(t *testing.T) {
	root := t.TempDir()
	listing := model.New("https://example.test/listing", "test", time.Unix(1, 0))
	listing.SchemaVersion = 1
	listing.Source.CanonicalURL = listing.Source.URL
	listing.Location.Country = "Country"
	listing.Location.Region = "Region"
	listing.Location.Municipality = "Municipality"
	listing.Location.Street = "10 Main St"
	dir := filepath.Join(root, "Country", "Region", "Municipality", "10 Main St")
	if err := os.MkdirAll(filepath.Join(dir, "images"), 0o755); err != nil {
		t.Fatal(err)
	}
	listing.Images = []model.Asset{{SourceURL: "https://example.test/one.jpg", File: "img-1.jpg"}}
	data, _ := json.Marshal(listing)
	if err := os.WriteFile(filepath.Join(dir, "listing.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "source.url"), []byte(listing.Source.URL+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "source.html"), []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "images", "img-1.jpg"), []byte("fixture"), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := MigrateArchive(root, time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if report.Migrated != 1 || report.DirectoriesIndexed == 0 {
		t.Fatalf("unexpected migration report: %#v", report)
	}
	migrated, err := readListing(filepath.Join(dir, "listing.json"))
	if err != nil {
		t.Fatal(err)
	}
	if migrated.SchemaVersion != model.SchemaVersion {
		t.Fatalf("schema version = %d", migrated.SchemaVersion)
	}
	if _, err := os.Stat(filepath.Join(dir, "manifest.json")); err != nil {
		t.Fatal(err)
	}
}

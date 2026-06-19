package testsuite

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"
	"listing-archiver/internal/site"
)

type Expectation struct {
	Title        string
	ListingID    string
	Country      string
	Municipality string
	ImageCount   int
	VideoCount   int
}

func RunFixtureExtraction(t *testing.T, extractor site.Extractor, fixturePath, pageURL string, expectation Expectation) {
	t.Helper()
	html, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(html)))
	if err != nil {
		t.Fatal(err)
	}
	listing, media, err := extractor.Extract(context.Background(), site.Page{URL: pageURL, HTML: html, Doc: doc})
	if err != nil {
		t.Fatal(err)
	}
	if expectation.Title != "" && listing.Property.Title != expectation.Title {
		t.Fatalf("title = %q, want %q", listing.Property.Title, expectation.Title)
	}
	if expectation.ListingID != "" && listing.Source.ListingID != expectation.ListingID {
		t.Fatalf("listing_id = %q, want %q", listing.Source.ListingID, expectation.ListingID)
	}
	if expectation.Country != "" && listing.Location.Country != expectation.Country {
		t.Fatalf("country = %q, want %q", listing.Location.Country, expectation.Country)
	}
	if expectation.Municipality != "" && listing.Location.Municipality != expectation.Municipality {
		t.Fatalf("municipality = %q, want %q", listing.Location.Municipality, expectation.Municipality)
	}
	if len(media.Images) != expectation.ImageCount {
		t.Fatalf("image count = %d, want %d", len(media.Images), expectation.ImageCount)
	}
	if len(media.Videos) != expectation.VideoCount {
		t.Fatalf("video count = %d, want %d", len(media.Videos), expectation.VideoCount)
	}
}

func FixedNow(unix int64) func() time.Time {
	return func() time.Time { return time.Unix(unix, 0).UTC() }
}

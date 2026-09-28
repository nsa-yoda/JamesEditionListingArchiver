package jamesedition

import (
	"path/filepath"
	"testing"

	"listing-archiver/internal/site/testsuite"
)

func TestExtractorConformanceFixtures(t *testing.T) {
	extractor := Extractor{Now: testsuite.FixedNow(1)}
	testsuite.RunFixtureExtraction(t, extractor,
		filepath.Join("testdata", "listing.html"),
		"https://www.jamesedition.com/real_estate/example-nj-usa/10-main-st-12345",
		testsuite.Expectation{
			Title:        "Structured house title",
			ListingID:    "12345",
			Country:      "US",
			Municipality: "Example",
			ImageCount:   1,
			VideoCount:   0,
		},
	)
	testsuite.RunFixtureExtraction(t, extractor,
		filepath.Join("testdata", "apartment.html"),
		"https://www.jamesedition.com/source",
		testsuite.Expectation{
			Title:        "Apartment with terrace",
			ListingID:    "JE-APT-42",
			Country:      "France",
			Municipality: "Paris",
			ImageCount:   2,
			VideoCount:   0,
		},
	)
}

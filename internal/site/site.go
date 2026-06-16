package site

import (
	"context"

	"github.com/PuerkitoBio/goquery"
	"listing-archiver/internal/model"
)

type Page struct {
	URL  string
	HTML []byte
	Doc  *goquery.Document
}

type Extractor interface {
	Match(rawURL string) bool
	Extract(ctx context.Context, page Page) (*model.Listing, []string, error)
}

func Select(rawURL string, extractors ...Extractor) Extractor {
	for _, extractor := range extractors {
		if extractor.Match(rawURL) {
			return extractor
		}
	}
	return nil
}

package jamesedition

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"
	"listing-archiver/internal/model"
	"listing-archiver/internal/site"
)

func TestFixtureExtraction(t *testing.T) {
	html, err := os.ReadFile("testdata/listing.html")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(html)))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	listing, media, err := (Extractor{Now: func() time.Time { return now }}).Extract(context.Background(), site.Page{
		URL: "https://www.jamesedition.com/real_estate/example-nj-usa/10-main-st-12345", HTML: html, Doc: doc,
	})
	if err != nil {
		t.Fatal(err)
	}
	if listing.SchemaVersion != model.SchemaVersion || listing.Source.ListingID != "12345" {
		t.Fatalf("unexpected source: %#v", listing.Source)
	}
	if listing.Source.CanonicalURL != "https://www.jamesedition.com/real_estate/example-nj-usa/10-main-st-12345" {
		t.Fatalf("canonical URL = %q", listing.Source.CanonicalURL)
	}
	if listing.Property.Price == nil || listing.Property.Price.Amount == nil || *listing.Property.Price.Amount != 1250000 {
		t.Fatalf("unexpected price: %#v", listing.Property.Price)
	}
	if listing.Property.Bedrooms == nil || *listing.Property.Bedrooms != 4 || listing.Location.Municipality != "Example" {
		t.Fatalf("unexpected listing: %#v", listing)
	}
	if len(media.Images) != 1 || media.Images[0] != "https://www.jamesedition.com/images/original.jpg" {
		t.Fatalf("unexpected media: %#v", media)
	}
}

func TestApartmentGraphFixtureExtraction(t *testing.T) {
	html, err := os.ReadFile("testdata/apartment.html")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(html)))
	if err != nil {
		t.Fatal(err)
	}
	listing, media, err := (Extractor{}).Extract(context.Background(), site.Page{
		URL: "https://www.jamesedition.com/source", HTML: html, Doc: doc,
	})
	if err != nil {
		t.Fatal(err)
	}
	if listing.Source.ListingID != "JE-APT-42" || listing.Location.Country != "France" {
		t.Fatalf("unexpected source/location: %#v %#v", listing.Source, listing.Location)
	}
	if listing.Broker.Agent != "Example Agent" || listing.Broker.Agency != "Example Realty" {
		t.Fatalf("unexpected broker: %#v", listing.Broker)
	}
	if listing.Source.CanonicalURL != "https://www.jamesedition.com/real_estate/paris-france/apartment-rue-example-98765" {
		t.Fatalf("canonical URL = %q", listing.Source.CanonicalURL)
	}
	if listing.Location.Municipality != "Paris" {
		t.Fatalf("municipality = %q", listing.Location.Municipality)
	}
	if len(media.Images) != 2 || media.Images[0] != "https://www.jamesedition.com/images/apartment-original.jpg" || media.Images[1] != "https://www.jamesedition.com/images/apartment-jsonld.jpg" {
		t.Fatalf("media = %#v", media)
	}
}

func TestLocationFallbacksUseCanonicalURLAndTitle(t *testing.T) {
	html := []byte(`<html><head>
		<link rel="canonical" href="https://www.jamesedition.com/real_estate/los-angeles-ca-usa/1509-amalfi-drive-17503065">
		<meta property="og:title" content="1509 Amalfi Drive In Los Angeles, California, United States For Sale">
	</head><body></body></html>`)
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(html)))
	if err != nil {
		t.Fatal(err)
	}
	listing, _, err := (Extractor{}).Extract(context.Background(), site.Page{
		URL: "https://www.jamesedition.com/source", HTML: html, Doc: doc,
	})
	if err != nil {
		t.Fatal(err)
	}
	if listing.Location.Municipality != "Los Angeles" || listing.Location.Region != "California" || listing.Location.Country != "United States" {
		t.Fatalf("location = %#v", listing.Location)
	}
	if listing.Location.Street != "1509 Amalfi Drive" {
		t.Fatalf("street = %q", listing.Location.Street)
	}
}

func TestLocationFallbacksUseUSCanonicalSlug(t *testing.T) {
	listing := model.New("", "jamesedition", time.Time{})
	locationFallbacks(&listing, "https://www.jamesedition.com/real_estate/fairview-nc-usa/listing-17831388")
	if listing.Location.Municipality != "Fairview" || listing.Location.Region != "North Carolina" || listing.Location.Country != "United States" {
		t.Fatalf("location = %#v", listing.Location)
	}
}

func TestLocationBreadcrumbFallbacksUseInternationalRegion(t *testing.T) {
	html := []byte(`<html><head>
		<link rel="canonical" href="https://www.jamesedition.com/real_estate/pasig-philippines/listing-15601413">
		<meta property="og:title" content="Apartment for sale">
	</head><body>
		<a href="/real_estate/pasig-philippines" id="back-to-search">Back to search</a>
		<div class="je2-breadcrumbs">
			<a href="/real_estate/philippines">Philippines</a>
			<a href="/real_estate/metro-manila-philippines">Metro Manila</a>
			<a href="/real_estate/pasig-philippines">Pasig</a>
		</div>
	</body></html>`)
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(html)))
	if err != nil {
		t.Fatal(err)
	}
	listing, _, err := (Extractor{}).Extract(context.Background(), site.Page{
		URL: "https://www.jamesedition.com/source", HTML: html, Doc: doc,
	})
	if err != nil {
		t.Fatal(err)
	}
	if listing.Location.Municipality != "Pasig" || listing.Location.Region != "Metro Manila" || listing.Location.Country != "Philippines" {
		t.Fatalf("location = %#v", listing.Location)
	}
}

func TestVisibleFallbacksCaptureFullDescriptionAndScopedFeatures(t *testing.T) {
	html := []byte(`<html><head>
		<link rel="canonical" href="https://www.jamesedition.com/real_estate/example-nj-usa/listing-12345">
		<meta property="og:description" content="Short description...">
	</head><body>
		<div class="je2-listing__section _about-property">
			<div class="je2-read-more__content _original">This is the complete saved listing description with considerably more detail.</div>
		</div>
		<div class="je2-listing__section _features">
			<div class="je2-listing-features">
				<ul><li>Privacy</li><li>Mountain View</li><li>Privacy</li></ul>
			</div>
		</div>
		<ul><li>Log in</li></ul>
		<a data-name="See on Google Maps" href="https://www.google.com/maps/search/?api=1&amp;query=40.1,-74.2">Map</a>
	</body></html>`)
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(html)))
	if err != nil {
		t.Fatal(err)
	}
	listing, _, err := (Extractor{}).Extract(context.Background(), site.Page{
		URL: "https://www.jamesedition.com/source", HTML: html, Doc: doc,
	})
	if err != nil {
		t.Fatal(err)
	}
	if listing.Property.Description != "This is the complete saved listing description with considerably more detail." {
		t.Fatalf("description = %q", listing.Property.Description)
	}
	if got := strings.Join(listing.Property.Features, ","); got != "Mountain View,Privacy" {
		t.Fatalf("features = %q", got)
	}
	if listing.Location.MapURL != "https://www.google.com/maps/search/?api=1&query=40.1,-74.2" {
		t.Fatalf("map URL = %q", listing.Location.MapURL)
	}
}

func TestMapSectionAddressFallback(t *testing.T) {
	html := []byte(`<html><head>
		<link rel="canonical" href="https://www.jamesedition.com/real_estate/oakville-canada/one-of-oakville-s-finest-lakefront-estates-16017322">
		<meta property="og:title" content="One Of Oakville's Finest Lakefront Estates. In Oakville, Ontario, Canada For Sale (16017322)">
	</head><body>
		<section class="je2-listing__section _map">
			<span>2054 Lakeshore Rd E, Oakville, ON L6J 1M3, Ontario, Canada</span>
			<a data-name="See on Google Maps" href="https://www.google.com/maps/search/?api=1&amp;query=43.4701061,-79.6416446">View on Google Maps</a>
		</section>
	</body></html>`)
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(html)))
	if err != nil {
		t.Fatal(err)
	}
	listing, _, err := (Extractor{}).Extract(context.Background(), site.Page{
		URL: "https://www.jamesedition.com/source", HTML: html, Doc: doc,
	})
	if err != nil {
		t.Fatal(err)
	}
	if listing.Location.Address != "2054 Lakeshore Rd E, Oakville, ON L6J 1M3, Ontario, Canada" ||
		listing.Location.Street != "2054 Lakeshore Rd E" ||
		listing.Location.PostalCode != "L6J 1M3" {
		t.Fatalf("location = %#v", listing.Location)
	}
}

func TestGoogleMapsURLRejectsUntrustedHost(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(`<a data-name="See on Google Maps" href="https://example.test/not-google">Map</a>`))
	if err != nil {
		t.Fatal(err)
	}
	if got := googleMapsURL(doc, "https://www.jamesedition.com/listing"); got != "" {
		t.Fatalf("map URL = %q", got)
	}
}

func TestGoogleMapsURLAcceptsGenericMapsLink(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(`<a href="https://www.google.com/maps/place/Test/@40.1,-74.2,10z">Map</a>`))
	if err != nil {
		t.Fatal(err)
	}
	if got := googleMapsURL(doc, "https://www.jamesedition.com/listing"); got != "https://www.google.com/maps/place/Test/@40.1,-74.2,10z" {
		t.Fatalf("map URL = %q", got)
	}
}

func TestDiscoverVideosCapturesPoster(t *testing.T) {
	html := []byte(`<html><head><meta property="og:image" content="/poster-fallback.jpg"></head><body><video poster="/poster.jpg"><source src="/tour.mp4" type="video/mp4"></video></body></html>`)
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(html)))
	if err != nil {
		t.Fatal(err)
	}
	media := discoverVideos(site.Page{URL: "https://www.jamesedition.com/listing", HTML: html, Doc: doc})
	if len(media) != 1 || media[0].SourceURL != "https://www.jamesedition.com/tour.mp4" || media[0].PosterURL != "https://www.jamesedition.com/poster.jpg" {
		t.Fatalf("media = %#v", media)
	}
}

func TestDiscoverVideosDecodesPlayerQueryEntities(t *testing.T) {
	html := []byte(`<html><body><iframe src="https://player.vimeo.com/video/1113641202?autoplay=1&amp;muted=1"></iframe></body></html>`)
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(html)))
	if err != nil {
		t.Fatal(err)
	}
	media := discoverVideos(site.Page{URL: "https://www.jamesedition.com/listing", HTML: html, Doc: doc})
	if len(media) != 1 || media[0].SourceURL != "https://player.vimeo.com/video/1113641202?autoplay=1&muted=1" {
		t.Fatalf("media = %#v", media)
	}
}

func TestDiscoverVideosFindsBrightcovePlayerInIframeDataSource(t *testing.T) {
	html := []byte(`<html><body><iframe data-src="https://players.brightcove.net/5782886755001/rJlOfaQNgQ_default/index.html?videoId=ref:15_second_property_clip_95efcf67-a67d-49b3-9d8d-1344055af2ac&amp;autoplay=muted&amp;loop=true" src="./listing_files/index.html"></iframe></body></html>`)
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(html)))
	if err != nil {
		t.Fatal(err)
	}
	media := discoverVideos(site.Page{URL: "https://www.jamesedition.com/listing", HTML: html, Doc: doc})
	want := "https://players.brightcove.net/5782886755001/rJlOfaQNgQ_default/index.html?videoId=ref:15_second_property_clip_95efcf67-a67d-49b3-9d8d-1344055af2ac&autoplay=muted&loop=true"
	if len(media) != 1 || media[0].SourceURL != want {
		t.Fatalf("media = %#v", media)
	}
}

func TestPrimaryListingVisibleDetailsOverrideRecommendationJSONLD(t *testing.T) {
	html := []byte(`<html><head>
		<link rel="canonical" href="https://www.jamesedition.com/real_estate/example-nj-usa/main-listing-12345">
		<script type="application/ld+json">{"@type":"Product","name":"Main listing","offers":{"price":4500000,"priceCurrency":"USD","availability":"https://schema.org/InStock"}}</script>
	</head><body>
		<div class="je2-listing__section _overview">
			<div class="je2-listing-info__price"><span>$4,500,000</span></div>
			<h1>10 Main St, Example, New Jersey 07000</h1>
			<ul class="je2-listing-info__specs"><li>6 Beds</li><li>6 Baths</li><li>7,054 Sqft</li><li>26,202 Sqft lot</li></ul>
			<button class="je2-listing-info__location" aria-label="Example, New Jersey, United States"></button>
		</div>
		<div class="je2-listing-about-building"><ul>
			<li><h3>Property type</h3><p>House</p></li>
			<li><h3>Floors</h3><p>2</p></li>
			<li><h3>Year built</h3><p>2025</p></li>
			<li><h3>Price/sqft</h3><p>$637</p></li>
		</ul></div>
		<div class="je2-listing-in-details">
			<button data-open="Photos"><img alt="All photos (121)"></button>
			<button data-open="Video"><iframe data-src="https://www.youtube.com/embed/example"></iframe></button>
		</div>
		<div class="je3-listing-contact-card__agent-or-office-info">
			<div class="je3-listing-contact-card__agent-or-office-info__name">Example Agent</div>
			<a aria-label="View agent profile" href="/agents/example-agent-1">Profile</a>
		</div>
		<div class="je2-listed-by-info__office-info">
			<div class="je2-listed-by-info__office-info__name">Example Agency</div>
			<div class="je2-listed-by-info__office-info__address">1 Agency Way</div>
			<a aria-label="View agency profile" href="/offices/example-agency-2">Profile</a>
		</div>
		<div class="je2-listed-by-info__info-blocks">
			<div class="je2-listed-by-info__info-block"><div class="je2-listed-by-info__info-block__label">First listed</div><div class="je2-listed-by-info__info-block__value">Oct 14, 2025</div></div>
			<div class="je2-listed-by-info__info-block"><div class="je2-listed-by-info__info-block__label">Last updated</div><div class="je2-listed-by-info__info-block__value">February 10</div></div>
			<div class="je2-listed-by-info__info-block"><div class="je2-listed-by-info__info-block__label">Agent licence</div><div class="je2-listed-by-info__info-block__value">#ABC</div></div>
			<div class="je2-listed-by-info__info-block"><div class="je2-listed-by-info__info-block__label">Listing reference</div><div class="je2-listed-by-info__info-block__value">REF-9</div></div>
		</div>
		<div class="ListingCard"><script type="application/ld+json">{"@type":"House","numberOfBedrooms":"3","numberOfBathroomsTotal":"3"}</script></div>
	</body></html>`)
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(html)))
	if err != nil {
		t.Fatal(err)
	}
	listing, _, err := (Extractor{}).Extract(context.Background(), site.Page{
		URL: "https://www.jamesedition.com/source", HTML: html, Doc: doc,
	})
	if err != nil {
		t.Fatal(err)
	}
	if *listing.Property.Bedrooms != 6 || *listing.Property.Bathrooms != 6 || *listing.Property.Floors != 2 {
		t.Fatalf("primary facts = %#v", listing.Property)
	}
	if listing.Location.Municipality != "Example" || listing.Location.Region != "New Jersey" || listing.Location.Country != "United States" {
		t.Fatalf("location = %#v", listing.Location)
	}
	if listing.Property.InteriorArea == nil || *listing.Property.InteriorArea.Value != 7054 || listing.Property.LotArea == nil || *listing.Property.LotArea.Value != 26202 {
		t.Fatalf("measurements = %#v %#v", listing.Property.InteriorArea, listing.Property.LotArea)
	}
	if listing.Property.Price.Display != "$4,500,000" || listing.Property.PricePerArea.Display != "$637" || listing.Property.Availability != "InStock" {
		t.Fatalf("prices/availability = %#v", listing.Property)
	}
	if *listing.Property.PhotoCount != 121 || listing.Property.VideoURL != "https://www.youtube.com/embed/example" {
		t.Fatalf("media = %#v", listing.Property)
	}
	if listing.Source.ListingReference != "REF-9" || listing.Source.FirstListed != "Oct 14, 2025" || listing.Source.LastUpdated != "February 10" {
		t.Fatalf("source = %#v", listing.Source)
	}
	if listing.Broker.Agent != "Example Agent" || listing.Broker.Agency != "Example Agency" || listing.Broker.AgentLicense != "#ABC" || listing.Broker.AgencyAddress != "1 Agency Way" {
		t.Fatalf("broker = %#v", listing.Broker)
	}
}

func TestDiscoverImagesExcludesRecommendationCards(t *testing.T) {
	html := []byte(`<html><body>
		<main><img src="https://img.jamesedition.com/listing_images/main/je/main.jpg"></main>
		<div class="ListingCard"><img src="https://img.jamesedition.com/listing_images/recommended/je/card.jpg"></div>
	</body></html>`)
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(html)))
	if err != nil {
		t.Fatal(err)
	}
	images := discoverImages(site.Page{URL: "https://www.jamesedition.com/listing", HTML: html, Doc: doc}, nil)
	if len(images) != 1 || !strings.Contains(images[0], "/main/") {
		t.Fatalf("images = %#v", images)
	}
}

func TestMatch(t *testing.T) {
	extractor := Extractor{}
	if !extractor.Match("https://www.jamesedition.com/real_estate/x") || extractor.Match("https://example.test/x") {
		t.Fatal("unexpected match result")
	}
}

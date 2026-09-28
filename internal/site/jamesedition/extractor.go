package jamesedition

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"listing-archiver/internal/extract"
	"listing-archiver/internal/model"
	"listing-archiver/internal/site"
)

type Extractor struct {
	Now func() time.Time
}

func (Extractor) Match(rawURL string) bool {
	u, err := url.Parse(rawURL)
	return err == nil && strings.EqualFold(strings.TrimPrefix(u.Hostname(), "www."), "jamesedition.com")
}

func (e Extractor) Extract(ctx context.Context, page site.Page) (*model.Listing, site.MediaURLs, error) {
	if err := ctx.Err(); err != nil {
		return nil, site.MediaURLs{}, err
	}
	now := time.Now
	if e.Now != nil {
		now = e.Now
	}
	listing := model.New(page.URL, "jamesedition", now())
	listing.Source.CanonicalURL = page.URL
	if canonical := canonicalURL(page.Doc); canonical != "" {
		if normalized, err := extract.NormalizeURL(page.URL, canonical); err == nil {
			listing.Source.CanonicalURL = normalized
		}
	}
	listing.Property.Title = firstNonEmpty(meta(page.Doc, "property", "og:title"), text(page.Doc.Find("h1").First()), text(page.Doc.Find("title").First()))
	listing.Property.Description = firstNonEmpty(meta(page.Doc, "property", "og:description"), meta(page.Doc, "name", "description"))

	var jsonLD []json.RawMessage
	var structuredImages []string
	page.Doc.Find(`script[type="application/ld+json"]`).Each(func(_ int, selection *goquery.Selection) {
		raw := json.RawMessage(strings.TrimSpace(selection.Text()))
		if json.Valid(raw) && selection.ParentsFiltered(".ListingCard").Length() == 0 {
			jsonLD = append(jsonLD, raw)
			var value any
			if json.Unmarshal(raw, &value) == nil {
				walkJSONLD(value, &listing)
				structuredImages = append(structuredImages, imageValues(value)...)
			}
		}
	})
	listing.Metadata = model.CloneMetadata(jsonLD)
	visibleFallbacks(page.Doc, &listing)
	locationBreadcrumbFallbacks(page.Doc, &listing)
	locationFallbacks(&listing, listing.Source.CanonicalURL)
	listing.Location.MapURL = googleMapsURL(page.Doc, page.URL)
	mapCoordinates(&listing.Location)
	mediaFallbacks(page.Doc, &listing, page.URL)
	brokerFallbacks(page.Doc, &listing, page.URL)
	listing.Source.ListingID = firstNonEmpty(listing.Source.ListingID, listingID(listing.Source.CanonicalURL))
	media := site.MediaURLs{
		Images: discoverImages(page, structuredImages),
		Videos: discoverVideos(page),
	}
	return &listing, media, nil
}

func walkJSONLD(value any, listing *model.Listing) {
	switch item := value.(type) {
	case []any:
		for _, child := range item {
			walkJSONLD(child, listing)
		}
	case map[string]any:
		if graph, ok := item["@graph"]; ok {
			walkJSONLD(graph, listing)
		}
		kind := strings.ToLower(typeString(item["@type"]))
		if isListingType(kind) {
			listing.Property.Title = firstNonEmpty(asString(item["name"]), asString(item["headline"]), listing.Property.Title)
			listing.Property.Description = firstNonEmpty(asString(item["description"]), listing.Property.Description)
			listing.Property.Type = firstNonEmpty(asString(item["accommodationCategory"]), asString(item["category"]), listing.Property.Type)
			listing.Property.Bedrooms = firstNumber(item["numberOfBedrooms"], listing.Property.Bedrooms)
			listing.Property.Bathrooms = firstNumber(item["numberOfBathroomsTotal"], item["numberOfBathrooms"], listing.Property.Bathrooms)
			listing.Property.InteriorArea = measurement(item["floorSize"], listing.Property.InteriorArea)
			listing.Property.LotArea = measurement(item["landSize"], listing.Property.LotArea)
			listing.Source.ListingID = firstNonEmpty(listing.Source.ListingID, identifier(item["identifier"]), asString(item["sku"]), asString(item["mpn"]))
			if year := number(item["yearBuilt"]); year != nil {
				value := int(*year)
				listing.Property.YearBuilt = &value
			}
			if address, ok := item["address"].(map[string]any); ok {
				listing.Location.Street = firstNonEmpty(listing.Location.Street, asString(address["streetAddress"]))
				listing.Location.Municipality = firstNonEmpty(listing.Location.Municipality, asString(address["addressLocality"]))
				listing.Location.Region = firstNonEmpty(listing.Location.Region, asString(address["addressRegion"]))
				listing.Location.PostalCode = firstNonEmpty(listing.Location.PostalCode, asString(address["postalCode"]))
				listing.Location.Country = firstNonEmpty(listing.Location.Country, countryString(address["addressCountry"]))
				listing.Location.Address = joinNonEmpty(", ", listing.Location.Street, listing.Location.Municipality, listing.Location.Region, listing.Location.PostalCode, listing.Location.Country)
			}
			if offers := firstObject(item["offers"]); offers != nil {
				display := asString(offers["price"])
				listing.Property.Price = &model.Money{Amount: number(offers["price"]), Currency: asString(offers["priceCurrency"]), Display: display}
				listing.Property.Availability = firstNonEmpty(listing.Property.Availability, availability(offers["availability"]))
			}
			listing.Broker.Agent = firstNonEmpty(listing.Broker.Agent, namedEntity(item["agent"]))
			listing.Broker.Agency = firstNonEmpty(listing.Broker.Agency, namedEntity(item["broker"]), namedEntity(item["provider"]), namedEntity(item["seller"]))
		}
		for _, child := range item {
			switch child.(type) {
			case map[string]any, []any:
				walkJSONLD(child, listing)
			}
		}
	}
}

func visibleFallbacks(doc *goquery.Document, listing *model.Listing) {
	allText := normalizeSpace(doc.Text())
	visibleOverviewFallbacks(doc, listing)
	mapAddressFallbacks(doc, listing)
	doc.Find(`.je2-listing__section._about-property .je2-read-more__content._original`).Each(func(_ int, selection *goquery.Selection) {
		listing.Property.Description = longestNonEmpty(listing.Property.Description, text(selection))
	})
	if listing.Property.Price == nil {
		display := firstMatch(allText, `(?m)([$€£]\s?[0-9][0-9,]*(?:\.[0-9]{2})?)`)
		if display != "" {
			listing.Property.Price = &model.Money{Amount: number(display), Display: display}
		}
	}
	if listing.Property.Bedrooms == nil {
		listing.Property.Bedrooms = number(firstMatch(allText, `(?i)\b([0-9]+)\s+Beds?\b`))
	}
	if listing.Property.Bathrooms == nil {
		listing.Property.Bathrooms = number(firstMatch(allText, `(?i)\b([0-9]+(?:\.[0-9]+)?)\s+Baths?\b`))
	}

	labels := map[string]func(string){
		"property type": func(value string) { listing.Property.Type = value },
		"floors":        func(value string) { listing.Property.Floors = number(value) },
		"year built": func(value string) {
			if parsed, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
				listing.Property.YearBuilt = &parsed
			}
		},
		"price/sqft": func(value string) { listing.Property.PricePerArea = unitPrice(value, "sqft") },
	}
	doc.Find("h2,h3,h4,dt,strong").Each(func(_ int, selection *goquery.Selection) {
		set, ok := labels[strings.ToLower(text(selection))]
		if !ok {
			return
		}
		if value := text(selection.Next()); value != "" && len(value) < 150 {
			set(value)
		}
	})
	if listing.Property.PricePerArea != nil && listing.Property.Price != nil && listing.Property.Price.Currency != "" {
		listing.Property.PricePerArea.Currency = listing.Property.Price.Currency
	}
	doc.Find(`.je2-listed-by-info__info-block`).Each(func(_ int, selection *goquery.Selection) {
		label := strings.ToLower(text(selection.Find(`.je2-listed-by-info__info-block__label`).First()))
		value := text(selection.Find(`.je2-listed-by-info__info-block__value`).First())
		switch label {
		case "first listed":
			listing.Source.FirstListed = firstNonEmpty(listing.Source.FirstListed, value)
		case "last updated":
			listing.Source.LastUpdated = firstNonEmpty(listing.Source.LastUpdated, value)
		case "agent licence", "agent license":
			listing.Broker.AgentLicense = firstNonEmpty(listing.Broker.AgentLicense, value)
		case "listing reference":
			listing.Source.ListingReference = firstNonEmpty(listing.Source.ListingReference, value)
		}
	})

	seen := map[string]bool{}
	for _, feature := range listing.Property.Features {
		seen[feature] = true
	}
	featureItems := doc.Find(`.je2-listing__section._features .je2-listing-features li`)
	featureItems.Each(func(_ int, selection *goquery.Selection) {
		value := text(selection)
		if value != "" && len(value) <= 100 && !seen[value] {
			seen[value] = true
			listing.Property.Features = append(listing.Property.Features, value)
		}
	})
	if featureItems.Length() == 0 {
		doc.Find("li").Each(func(_ int, selection *goquery.Selection) {
			value := text(selection)
			if isFeature(value) && !seen[value] {
				seen[value] = true
				listing.Property.Features = append(listing.Property.Features, value)
			}
		})
	}
	sort.Strings(listing.Property.Features)
}

func visibleOverviewFallbacks(doc *goquery.Document, listing *model.Listing) {
	if display := text(doc.Find(`.je2-listing__section._overview .je2-listing-info__price span`).First()); display != "" {
		if listing.Property.Price == nil {
			listing.Property.Price = &model.Money{Amount: number(display)}
		}
		listing.Property.Price.Display = display
		if listing.Property.Price.Currency == "" {
			listing.Property.Price.Currency = currencyFromDisplay(display)
		}
	}
	if title := text(doc.Find(`.je2-listing__section._overview h1`).First()); title != "" {
		listing.Property.Title = title
		if comma := strings.Index(title, ","); comma > 0 {
			listing.Location.Street = firstNonEmpty(listing.Location.Street, strings.TrimSpace(title[:comma]))
		}
		if listing.Location.PostalCode == "" {
			listing.Location.PostalCode = firstMatch(title, `\b([0-9]{5}(?:-[0-9]{4})?)$`)
		}
	}
	locationLabel, _ := doc.Find(`.je2-listing__section._overview .je2-listing-info__location`).First().Attr("aria-label")
	locationParts := strings.Split(locationLabel, ",")
	if len(locationParts) >= 3 {
		locationParts = locationParts[len(locationParts)-3:]
		listing.Location.Municipality = strings.TrimSpace(locationParts[0])
		listing.Location.Region = strings.TrimSpace(locationParts[1])
		listing.Location.Country = strings.TrimSpace(locationParts[2])
	}
	doc.Find(`.je2-listing__section._overview .je2-listing-info__specs li`).Each(func(_ int, selection *goquery.Selection) {
		value := text(selection)
		lower := strings.ToLower(value)
		switch {
		case strings.Contains(lower, "bed"):
			listing.Property.Bedrooms = number(value)
		case strings.Contains(lower, "bath"):
			listing.Property.Bathrooms = number(value)
		case strings.Contains(lower, "lot"):
			listing.Property.LotArea = measurementFromDisplay(value)
		default:
			if parsed := measurementFromDisplay(value); parsed != nil {
				listing.Property.InteriorArea = parsed
			}
		}
	})
}

func mapAddressFallbacks(doc *goquery.Document, listing *model.Listing) {
	doc.Find(`a[data-name="See on Google Maps"]`).EachWithBreak(func(_ int, selection *goquery.Selection) bool {
		candidate := text(selection.PrevAllFiltered("span").First())
		if candidate == "" {
			candidate = text(selection.Parent().Find("span").First())
		}
		if candidate == "" || strings.EqualFold(candidate, "View on Google Maps") {
			return true
		}
		applyAddressCandidate(listing, candidate)
		return false
	})
}

func applyAddressCandidate(listing *model.Listing, candidate string) {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return
	}
	if listing.Location.Address == "" || strings.EqualFold(strings.TrimSuffix(listing.Location.Address, "."), strings.TrimSuffix(listing.Property.Title, ".")) {
		listing.Location.Address = candidate
	}
	parts := splitAddressParts(candidate)
	if len(parts) >= 5 {
		listing.Location.Street = firstNonEmpty(listing.Location.Street, parts[0])
		listing.Location.Municipality = firstNonEmpty(listing.Location.Municipality, parts[1])
		listing.Location.Region = firstNonEmpty(listing.Location.Region, parts[len(parts)-2])
		listing.Location.Country = firstNonEmpty(listing.Location.Country, parts[len(parts)-1])
	} else if len(parts) >= 4 {
		listing.Location.Street = firstNonEmpty(listing.Location.Street, parts[0])
		listing.Location.Municipality = firstNonEmpty(listing.Location.Municipality, parts[1])
		listing.Location.Region = firstNonEmpty(listing.Location.Region, parts[2])
		listing.Location.Country = firstNonEmpty(listing.Location.Country, parts[3])
	} else if len(parts) >= 2 {
		listing.Location.Street = firstNonEmpty(listing.Location.Street, parts[0])
	}
	if listing.Location.PostalCode == "" {
		listing.Location.PostalCode = firstNonEmpty(
			firstMatch(candidate, `\b([A-Z]\d[A-Z][ -]?\d[A-Z]\d)\b`),
			firstMatch(candidate, `\b([0-9]{5}(?:-[0-9]{4})?)\b`),
		)
	}
}

func splitAddressParts(value string) []string {
	var parts []string
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}

func mediaFallbacks(doc *goquery.Document, listing *model.Listing, baseURL string) {
	photoText, _ := doc.Find(`img[alt^="All photos ("]`).First().Attr("alt")
	if count := number(photoText); count != nil {
		value := int(*count)
		listing.Property.PhotoCount = &value
	}
	raw, ok := doc.Find(`[data-open="Video"] iframe`).First().Attr("data-src")
	if !ok {
		raw, ok = doc.Find(`[data-open="Video"] iframe`).First().Attr("src")
	}
	if ok {
		listing.Property.VideoURL = trustedURL(baseURL, raw, "youtube.com", "youtu.be", "vimeo.com", "brightcove.net")
	}
	if listing.Property.VideoURL == "" {
		for _, property := range []string{"og:video", "og:video:url", "og:video:secure_url"} {
			if value := meta(doc, "property", property); value != "" {
				listing.Property.VideoURL = trustedURL(baseURL, value, "youtube.com", "youtu.be", "vimeo.com", "brightcove.net")
				if listing.Property.VideoURL != "" {
					break
				}
			}
		}
	}
}

func brokerFallbacks(doc *goquery.Document, listing *model.Listing, baseURL string) {
	listing.Broker.Agent = firstNonEmpty(listing.Broker.Agent, text(doc.Find(`.je3-listing-contact-card__agent-or-office-info__name`).First()))
	listing.Broker.Agency = firstNonEmpty(listing.Broker.Agency, text(doc.Find(`.je2-listed-by-info__office-info__name`).First()))
	listing.Broker.AgencyAddress = firstNonEmpty(listing.Broker.AgencyAddress, text(doc.Find(`.je2-listed-by-info__office-info__address`).First()))
	if href, ok := doc.Find(`a[aria-label="View agent profile"]`).First().Attr("href"); ok {
		listing.Broker.AgentProfileURL = trustedURL(baseURL, href, "jamesedition.com")
	}
	if href, ok := doc.Find(`a[aria-label="View agency profile"]`).First().Attr("href"); ok {
		listing.Broker.AgencyProfileURL = trustedURL(baseURL, href, "jamesedition.com")
	}
}

func discoverImages(page site.Page, structured []string) []string {
	var candidates []string
	page.Doc.Find("img,source").Each(func(_ int, selection *goquery.Selection) {
		if selection.ParentsFiltered(".ListingCard, .je2-listed-by, .je3-listing-contact-card, .je2-agent-info, header, footer").Length() > 0 {
			return
		}
		highest := ""
		for _, attr := range []string{"srcset", "data-srcset"} {
			if value, ok := selection.Attr(attr); ok {
				highest = extract.HighestSrcset(value)
				if highest != "" {
					break
				}
			}
		}
		if highest != "" {
			candidates = append(candidates, highest)
			return
		}
		for _, attr := range []string{"data-original", "data-src", "data-lazy-src", "src"} {
			if value, ok := selection.Attr(attr); ok && strings.TrimSpace(value) != "" {
				candidates = append(candidates, value)
				break
			}
		}
	})
	for _, property := range []string{"og:image", "og:image:secure_url"} {
		if value := meta(page.Doc, "property", property); value != "" {
			candidates = append(candidates, value)
		}
	}
	re := regexp.MustCompile(`https?:\\?/\\?/[^"'<>[:space:]\\]+`)
	candidates = append(candidates, re.FindAllString(primaryListingHTML(page.HTML), -1)...)
	candidates = append(candidates, structured...)
	normalized := extract.NormalizeAndDedupe(page.URL, candidates)
	out := normalized[:0]
	for _, candidate := range normalized {
		lower := strings.ToLower(candidate)
		if looksLikeImageURL(lower) && !strings.Contains(lower, "logo") && !strings.Contains(lower, "avatar") && !strings.Contains(lower, "icon") {
			out = append(out, candidate)
		}
	}
	return out
}

func discoverVideos(page site.Page) []site.VideoURL {
	type candidate struct {
		source string
		poster string
	}
	var candidates []candidate
	page.Doc.Find("video,video source,a[href]").Each(func(_ int, selection *goquery.Selection) {
		if selection.ParentsFiltered(".ListingCard, .je2-listed-by, .je3-listing-contact-card, .je2-agent-info, header, footer").Length() > 0 {
			return
		}
		video := selection
		if goquery.NodeName(selection) != "video" {
			video = selection.Closest("video")
		}
		poster := ""
		for _, attr := range []string{"poster", "data-poster", "data-thumb"} {
			if value, ok := video.Attr(attr); ok && strings.TrimSpace(value) != "" {
				poster = value
				break
			}
		}
		for _, attr := range []string{"src", "data-src", "href"} {
			if value, ok := selection.Attr(attr); ok && strings.TrimSpace(value) != "" {
				candidates = append(candidates, candidate{source: value, poster: poster})
				break
			}
		}
	})
	posterFallback := firstNonEmpty(
		meta(page.Doc, "property", "og:image:secure_url"),
		meta(page.Doc, "property", "og:image"),
	)
	for _, property := range []string{"og:video", "og:video:url", "og:video:secure_url"} {
		if value := meta(page.Doc, "property", property); value != "" {
			candidates = append(candidates, candidate{source: value, poster: posterFallback})
		}
	}
	re := regexp.MustCompile(`https?:\\?/\\?/[^"'<>[:space:]\\]+`)
	for _, value := range re.FindAllString(primaryListingHTML(page.HTML), -1) {
		candidates = append(candidates, candidate{source: value, poster: posterFallback})
	}
	seen := map[string]bool{}
	var out []site.VideoURL
	for _, item := range candidates {
		source, err := extract.NormalizeURL(page.URL, html.UnescapeString(item.source))
		if err != nil || seen[source] || !looksLikeVideoURL(source) {
			continue
		}
		seen[source] = true
		video := site.VideoURL{SourceURL: source}
		if item.poster != "" {
			if poster, err := extract.NormalizeURL(page.URL, item.poster); err == nil && looksLikeImageURL(strings.ToLower(poster)) {
				video.PosterURL = poster
			}
		}
		out = append(out, video)
	}
	return out
}

func primaryListingHTML(html []byte) string {
	value := string(html)
	end := len(value)
	for _, marker := range []string{`class="ListingCard`, `je2-listing__section _similar-homes`, `je2-listing__section _new-listings`, `je2-listing__section _recently-viewed`} {
		if index := strings.Index(value, marker); index >= 0 && index < end {
			end = index
		}
	}
	return value[:end]
}

func canonicalURL(doc *goquery.Document) string {
	value, _ := doc.Find(`link[rel="canonical"]`).First().Attr("href")
	return strings.TrimSpace(value)
}

func googleMapsURL(doc *goquery.Document, baseURL string) string {
	var out string
	doc.Find(`a[href]`).EachWithBreak(func(_ int, selection *goquery.Selection) bool {
		href, ok := selection.Attr("href")
		if !ok {
			return true
		}
		normalized, err := extract.NormalizeURL(baseURL, href)
		if err != nil {
			return true
		}
		u, err := url.Parse(normalized)
		if err != nil {
			return true
		}
		host := strings.ToLower(u.Hostname())
		path := strings.ToLower(u.EscapedPath())
		switch {
		case host == "maps.app.goo.gl":
			out = normalized
			return false
		case (host == "google.com" || strings.HasSuffix(host, ".google.com")) &&
			(strings.Contains(path, "/maps") || strings.Contains(u.RawQuery, "maps")):
			out = normalized
			return false
		default:
			return true
		}
	})
	return out
}

func mapCoordinates(location *model.Location) {
	u, err := url.Parse(location.MapURL)
	if err != nil {
		return
	}
	parts := strings.Split(u.Query().Get("query"), ",")
	if len(parts) != 2 {
		return
	}
	location.Latitude = number(parts[0])
	location.Longitude = number(parts[1])
}

func trustedURL(baseURL, raw string, domains ...string) string {
	normalized, err := extract.NormalizeURL(baseURL, raw)
	if err != nil {
		return ""
	}
	u, err := url.Parse(normalized)
	if err != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	for _, domain := range domains {
		domain = strings.ToLower(domain)
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return normalized
		}
	}
	return ""
}

func locationFallbacks(listing *model.Listing, rawURL string) {
	locationFromTitle(listing)

	u, err := url.Parse(rawURL)
	if err == nil {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) >= 3 && parts[0] == "real_estate" {
			location := strings.Split(parts[1], "-")
			if listing.Location.Country == "" && len(location) > 0 {
				listing.Location.Country = titleWords(location[len(location)-1])
				if strings.EqualFold(listing.Location.Country, "usa") {
					listing.Location.Country = "United States"
				}
			}
			if strings.EqualFold(location[len(location)-1], "usa") && len(location) >= 3 {
				regionCode := strings.ToUpper(location[len(location)-2])
				listing.Location.Region = firstNonEmpty(listing.Location.Region, usRegionNames[regionCode], regionCode)
				listing.Location.Municipality = firstNonEmpty(listing.Location.Municipality, titleWords(strings.Join(location[:len(location)-2], " ")))
			} else if len(location) >= 2 {
				listing.Location.Municipality = firstNonEmpty(listing.Location.Municipality, titleWords(strings.Join(location[:len(location)-1], " ")))
			}
			if listing.Location.Street == "" {
				slug := regexp.MustCompile(`-\d+$`).ReplaceAllString(parts[2], "")
				words := strings.Split(slug, "-")
				if len(words) > 0 && number(words[0]) != nil {
					listing.Location.Street = titleWords(strings.Join(words[:min(4, len(words))], " "))
				}
			}
		}
	}
}

func locationFromTitle(listing *model.Listing) {
	title := strings.TrimSpace(listing.Property.Title)
	lower := strings.ToLower(title)
	if !strings.HasSuffix(lower, " for sale") {
		return
	}
	title = strings.TrimSpace(title[:len(title)-len(" for sale")])
	index := strings.LastIndex(strings.ToLower(title), " in ")
	if index < 0 {
		return
	}
	parts := strings.Split(title[index+len(" in "):], ",")
	if len(parts) < 3 {
		return
	}
	parts = parts[len(parts)-3:]
	listing.Location.Municipality = firstNonEmpty(listing.Location.Municipality, strings.TrimSpace(parts[0]))
	listing.Location.Region = firstNonEmpty(listing.Location.Region, strings.TrimSpace(parts[1]))
	listing.Location.Country = firstNonEmpty(listing.Location.Country, strings.TrimSpace(parts[2]))
}

func locationBreadcrumbFallbacks(doc *goquery.Document, listing *model.Listing) {
	var labels []string
	doc.Find(`.je2-breadcrumbs a[href*="/real_estate/"]`).Each(func(_ int, selection *goquery.Selection) {
		href, ok := selection.Attr("href")
		if !ok {
			return
		}
		link, err := url.Parse(href)
		if err != nil {
			return
		}
		linkParts := strings.Split(strings.Trim(link.Path, "/"), "/")
		if len(linkParts) != 2 || linkParts[0] != "real_estate" {
			return
		}
		label := text(selection)
		if label != "" && len(label) <= 100 {
			labels = append(labels, label)
		}
	})
	if len(labels) >= 3 {
		labels = labels[len(labels)-3:]
		listing.Location.Country = firstNonEmpty(listing.Location.Country, labels[0])
		listing.Location.Region = firstNonEmpty(listing.Location.Region, labels[1])
		listing.Location.Municipality = firstNonEmpty(listing.Location.Municipality, labels[2])
	} else if len(labels) == 2 {
		listing.Location.Country = firstNonEmpty(listing.Location.Country, labels[0])
		listing.Location.Municipality = firstNonEmpty(listing.Location.Municipality, labels[1])
	}
}

var usRegionNames = map[string]string{
	"AK": "Alaska", "AL": "Alabama", "AR": "Arkansas", "AS": "American Samoa",
	"AZ": "Arizona", "CA": "California", "CO": "Colorado", "CT": "Connecticut",
	"DC": "District of Columbia", "DE": "Delaware", "FL": "Florida", "GA": "Georgia",
	"GU": "Guam", "HI": "Hawaii", "IA": "Iowa", "ID": "Idaho", "IL": "Illinois",
	"IN": "Indiana", "KS": "Kansas", "KY": "Kentucky", "LA": "Louisiana",
	"MA": "Massachusetts", "MD": "Maryland", "ME": "Maine", "MI": "Michigan",
	"MN": "Minnesota", "MO": "Missouri", "MP": "Northern Mariana Islands",
	"MS": "Mississippi", "MT": "Montana", "NC": "North Carolina", "ND": "North Dakota",
	"NE": "Nebraska", "NH": "New Hampshire", "NJ": "New Jersey", "NM": "New Mexico",
	"NV": "Nevada", "NY": "New York", "OH": "Ohio", "OK": "Oklahoma", "OR": "Oregon",
	"PA": "Pennsylvania", "PR": "Puerto Rico", "RI": "Rhode Island",
	"SC": "South Carolina", "SD": "South Dakota", "TN": "Tennessee", "TX": "Texas",
	"UT": "Utah", "VA": "Virginia", "VI": "U.S. Virgin Islands", "VT": "Vermont",
	"WA": "Washington", "WI": "Wisconsin", "WV": "West Virginia", "WY": "Wyoming",
}

func listingID(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	match := regexp.MustCompile(`-(\d+)$`).FindStringSubmatch(strings.Trim(u.Path, "/"))
	if len(match) == 2 {
		return match[1]
	}
	return ""
}

func measurement(value any, fallback *model.Measurement) *model.Measurement {
	item, ok := value.(map[string]any)
	if !ok {
		return fallback
	}
	display := joinNonEmpty(" ", asString(item["value"]), asString(item["unitText"]))
	return &model.Measurement{Value: number(item["value"]), Unit: asString(item["unitText"]), Display: display}
}

func measurementFromDisplay(display string) *model.Measurement {
	value := number(display)
	if value == nil {
		return nil
	}
	lower := strings.ToLower(display)
	unit := ""
	for _, candidate := range []string{"sqft", "sq ft", "ft²", "sqm", "sq m", "m²", "acre", "hectare"} {
		if strings.Contains(lower, candidate) {
			unit = candidate
			break
		}
	}
	return &model.Measurement{Value: value, Unit: unit, Display: strings.TrimSpace(display)}
}

func unitPrice(display, perUnit string) *model.UnitPrice {
	if strings.TrimSpace(display) == "" {
		return nil
	}
	return &model.UnitPrice{
		Amount: number(display), Currency: currencyFromDisplay(display),
		PerUnit: perUnit, Display: strings.TrimSpace(display),
	}
}

func currencyFromDisplay(display string) string {
	switch {
	case strings.Contains(display, "$"):
		return "USD"
	case strings.Contains(display, "€"):
		return "EUR"
	case strings.Contains(display, "£"):
		return "GBP"
	}
	return ""
}

func availability(value any) string {
	raw := asString(value)
	if index := strings.LastIndexAny(raw, "/#"); index >= 0 {
		raw = raw[index+1:]
	}
	return raw
}

func firstNumber(values ...any) *float64 {
	for _, value := range values {
		if existing, ok := value.(*float64); ok && existing != nil {
			return existing
		}
		if parsed := number(value); parsed != nil {
			return parsed
		}
	}
	return nil
}

func number(value any) *float64 {
	raw := strings.NewReplacer(",", "", "$", "", "€", "", "£", "").Replace(asString(value))
	match := regexp.MustCompile(`-?[0-9]+(?:\.[0-9]+)?`).FindString(raw)
	if match == "" {
		return nil
	}
	parsed, err := strconv.ParseFloat(match, 64)
	if err != nil {
		return nil
	}
	return &parsed
}

func isListingType(kind string) bool {
	for _, value := range []string{"house", "residence", "product", "accommodation", "realestate", "apartment"} {
		if strings.Contains(kind, value) {
			return true
		}
	}
	return false
}

func meta(doc *goquery.Document, key, value string) string {
	content, _ := doc.Find(fmt.Sprintf(`meta[%s="%s"]`, key, value)).First().Attr("content")
	return strings.TrimSpace(content)
}
func text(selection *goquery.Selection) string { return normalizeSpace(selection.Text()) }
func normalizeSpace(value string) string       { return strings.Join(strings.Fields(value), " ") }
func firstMatch(value, pattern string) string {
	match := regexp.MustCompile(pattern).FindStringSubmatch(value)
	if len(match) > 1 {
		return strings.TrimSpace(match[1])
	}
	return ""
}
func asString(value any) string {
	switch item := value.(type) {
	case string:
		return strings.TrimSpace(item)
	case float64:
		return strconv.FormatFloat(item, 'f', -1, 64)
	case json.Number:
		return item.String()
	}
	return ""
}
func countryString(value any) string {
	if item, ok := value.(map[string]any); ok {
		return firstNonEmpty(asString(item["name"]), asString(item["identifier"]))
	}
	return asString(value)
}
func identifier(value any) string {
	if item, ok := value.(map[string]any); ok {
		return firstNonEmpty(asString(item["value"]), asString(item["name"]), asString(item["@id"]))
	}
	return asString(value)
}
func typeString(value any) string {
	if values, ok := value.([]any); ok {
		var out []string
		for _, item := range values {
			if text := asString(item); text != "" {
				out = append(out, text)
			}
		}
		return strings.Join(out, " ")
	}
	return asString(value)
}
func firstObject(value any) map[string]any {
	if item, ok := value.(map[string]any); ok {
		return item
	}
	if values, ok := value.([]any); ok {
		for _, value := range values {
			if item, ok := value.(map[string]any); ok {
				return item
			}
		}
	}
	return nil
}
func imageValues(value any) []string {
	var out []string
	switch item := value.(type) {
	case []any:
		for _, child := range item {
			out = append(out, imageValues(child)...)
		}
	case map[string]any:
		for key, child := range item {
			switch strings.ToLower(key) {
			case "image", "photo", "photos":
				out = append(out, stringValues(child)...)
			default:
				switch child.(type) {
				case map[string]any, []any:
					out = append(out, imageValues(child)...)
				}
			}
		}
	}
	return out
}
func stringValues(value any) []string {
	switch item := value.(type) {
	case string:
		return []string{item}
	case []any:
		var out []string
		for _, child := range item {
			out = append(out, stringValues(child)...)
		}
		return out
	case map[string]any:
		for _, key := range []string{"contentUrl", "url", "@id"} {
			if text := asString(item[key]); text != "" {
				return []string{text}
			}
		}
	}
	return nil
}
func namedEntity(value any) string {
	if item, ok := value.(map[string]any); ok {
		return firstNonEmpty(asString(item["name"]), asString(item["legalName"]))
	}
	return asString(value)
}
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
func longestNonEmpty(values ...string) string {
	longest := ""
	for _, value := range values {
		value = strings.TrimSpace(value)
		if len(value) > len(longest) {
			longest = value
		}
	}
	return longest
}
func joinNonEmpty(separator string, values ...string) string {
	var out []string
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, strings.TrimSpace(value))
		}
	}
	return strings.Join(out, separator)
}
func titleWords(value string) string {
	words := strings.Fields(strings.ReplaceAll(value, "-", " "))
	for index, word := range words {
		words[index] = strings.ToUpper(word[:1]) + strings.ToLower(word[1:])
	}
	return strings.Join(words, " ")
}
func isFeature(value string) bool {
	if value == "" || len(value) > 80 {
		return false
	}
	lower := strings.ToLower(value)
	for _, keyword := range []string{"garage", "fireplace", "pool", "garden", "cinema", "kitchen", "closet", "air conditioning", "laundry", "basement", "parking", "terrace", "sauna", "outdoor"} {
		if strings.Contains(lower, keyword) {
			return true
		}
	}
	return false
}

func looksLikeImageURL(value string) bool {
	return regexp.MustCompile(`\.(?:jpe?g|png|webp|avif|gif|svg)(?:$|[?])`).MatchString(value) ||
		strings.Contains(value, "/image/") || strings.Contains(value, "/images/")
}

func looksLikeVideoURL(value string) bool {
	lower := strings.ToLower(value)
	if regexp.MustCompile(`\.(?:mp4|webm)(?:$|[?])`).MatchString(lower) || strings.Contains(lower, "/video/") || strings.Contains(lower, "/videos/") {
		return true
	}
	u, err := url.Parse(value)
	return err == nil && strings.EqualFold(u.Hostname(), "players.brightcove.net") && u.Query().Get("videoId") != ""
}

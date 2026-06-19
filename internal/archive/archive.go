package archive

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"listing-archiver/internal/downloader"
	"listing-archiver/internal/model"
	"listing-archiver/internal/pathutil"
	"listing-archiver/internal/site"
)

type Service struct {
	Client     *downloader.Client
	Extractors []site.Extractor
}

type Result struct {
	Directory        string
	Listing          model.Listing
	DiscoveredImages int
	DiscoveredVideos int
	NewAssets        int
	ReusedAssets     int
	FailedAssets     int
}

var ErrArchiveCollision = errors.New("archive path collision")

type Options struct {
	Root         string
	Workers      int
	Overwrite    bool
	MetadataOnly bool
	MaxImages    int
	AssetsOnly   bool
}

type ExistingIndex struct {
	byKey map[string]string
}

func (s Service) Run(ctx context.Context, rawURL string, options Options) (Result, error) {
	if s.Client == nil {
		return Result{}, fmt.Errorf("HTTP client is required to fetch listing URL")
	}
	if err := downloader.SafeURL(rawURL); err != nil {
		return Result{}, fmt.Errorf("invalid listing URL: %w", err)
	}
	html, finalURL, err := s.Client.FetchPage(ctx, rawURL)
	if err != nil {
		return Result{}, err
	}
	return s.process(ctx, rawURL, finalURL, html, options)
}

func (s Service) ImportHTML(ctx context.Context, sourceURL string, html []byte, options Options) (Result, error) {
	if err := validateImportedHTML(sourceURL, html); err != nil {
		return Result{}, err
	}
	return s.process(ctx, sourceURL, sourceURL, html, options)
}

func (s Service) InspectHTML(ctx context.Context, sourceURL string, html []byte) (model.Listing, int, error) {
	if err := validateImportedHTML(sourceURL, html); err != nil {
		return model.Listing{}, 0, err
	}
	listing, mediaURLs, err := s.extract(ctx, sourceURL, sourceURL, html)
	if err != nil {
		return model.Listing{}, 0, err
	}
	return *listing, len(mediaURLs.Images) + len(mediaURLs.Videos), nil
}

func validateImportedHTML(sourceURL string, html []byte) error {
	if err := downloader.SafeURL(sourceURL); err != nil {
		return fmt.Errorf("invalid source URL: %w", err)
	}
	if len(html) == 0 {
		return fmt.Errorf("imported HTML is empty")
	}
	lowerHTML := strings.ToLower(string(html))
	if strings.Contains(lowerHTML, "cf-chl-") || strings.Contains(lowerHTML, "<title>just a moment") {
		return fmt.Errorf("imported HTML appears to be a Cloudflare challenge page, not a listing")
	}
	return nil
}

func (s Service) process(ctx context.Context, rawURL, finalURL string, html []byte, options Options) (Result, error) {
	if !options.MetadataOnly && s.Client == nil {
		return Result{}, fmt.Errorf("HTTP client is required to download imported listing images")
	}
	extracted, mediaURLs, err := s.extract(ctx, rawURL, finalURL, html)
	if err != nil {
		return Result{}, err
	}
	listing := extracted
	var directory string
	if options.AssetsOnly {
		if existing, err := BuildExistingIndex(options.Root); err == nil {
			if found, ok := existing.Find(*listing); ok {
				directory = found
			}
		} else {
			return Result{}, fmt.Errorf("inspect existing archives for assets-only refresh: %w", err)
		}
	}
	if directory == "" {
		directory, err = resolveListingDirectory(options.Root, *listing)
		if err != nil {
			return Result{}, err
		}
	}
	imagesDir := filepath.Join(directory, "images")
	if err := os.MkdirAll(imagesDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("create archive directory: %w", err)
	}
	if err := writeAtomic(filepath.Join(directory, "source.html"), html, 0o644); err != nil {
		return Result{}, fmt.Errorf("write source HTML: %w", err)
	}
	if err := writeAtomic(filepath.Join(directory, "source.url"), []byte(finalURL+"\n"), 0o644); err != nil {
		return Result{}, fmt.Errorf("write source URL: %w", err)
	}
	lookups, err := loadExistingAssetLookups(directory)
	if err != nil {
		return Result{}, err
	}
	if options.AssetsOnly {
		if existing, err := readListing(filepath.Join(directory, "listing.json")); err == nil {
			existing.Source.URL = rawURL
			if existing.Source.CanonicalURL == "" {
				existing.Source.CanonicalURL = listing.Source.CanonicalURL
			}
			existing.Source.RetrievedAt = listing.Source.RetrievedAt
			listing = &existing
		} else if !os.IsNotExist(err) {
			return Result{}, fmt.Errorf("read existing listing for assets-only refresh: %w", err)
		}
	}
	discoveredImages := len(mediaURLs.Images)
	discoveredVideos := len(mediaURLs.Videos)
	if options.MaxImages > 0 && len(mediaURLs.Images) > options.MaxImages {
		listing.Warnings = append(listing.Warnings, fmt.Sprintf("image downloads limited to %d of %d discovered URLs", options.MaxImages, len(mediaURLs.Images)))
		mediaURLs.Images = mediaURLs.Images[:options.MaxImages]
	}
	if options.MetadataOnly {
		for _, imageURL := range mediaURLs.Images {
			listing.Images = append(listing.Images, model.Asset{SourceURL: imageURL, Status: "discovered"})
		}
		for _, videoURL := range mediaURLs.Videos {
			listing.Videos = append(listing.Videos, model.Asset{
				SourceURL:       videoURL.SourceURL,
				PosterSourceURL: videoURL.PosterURL,
				Status:          "discovered",
			})
		}
	} else {
		listing.Images = s.Client.DownloadImages(ctx, mediaURLs.Images, imagesDir, downloader.ImageOptions{
			Workers: options.Workers, Overwrite: options.Overwrite, Referer: finalURL, Existing: lookups.Images,
		})
		if len(mediaURLs.Videos) > 0 {
			videosDir := filepath.Join(directory, "videos")
			if err := os.MkdirAll(videosDir, 0o755); err != nil {
				return Result{}, fmt.Errorf("create video archive directory: %w", err)
			}
			videoURLs := make([]string, 0, len(mediaURLs.Videos))
			videoPosters := map[string]string{}
			for _, video := range mediaURLs.Videos {
				videoURLs = append(videoURLs, video.SourceURL)
				if video.PosterURL != "" {
					videoPosters[video.SourceURL] = video.PosterURL
				}
			}
			listing.Videos = s.Client.DownloadVideos(ctx, videoURLs, videosDir, downloader.ImageOptions{
				Workers: options.Workers, Overwrite: options.Overwrite, Referer: finalURL, Existing: lookups.Videos,
			})
			for index := range listing.Videos {
				if posterURL := videoPosters[listing.Videos[index].SourceURL]; posterURL != "" {
					listing.Videos[index].PosterSourceURL = posterURL
				}
			}
			if err := s.downloadVideoPosters(ctx, videosDir, finalURL, options, lookups, listing.Videos); err != nil {
				return Result{}, err
			}
		}
	}
	for _, image := range listing.Images {
		if image.Error != "" {
			listing.Warnings = append(listing.Warnings, fmt.Sprintf("image %s: %s", image.SourceURL, image.Error))
		}
	}
	for _, video := range listing.Videos {
		if video.Error != "" {
			listing.Warnings = append(listing.Warnings, fmt.Sprintf("video %s: %s", video.SourceURL, video.Error))
		}
	}
	metadata, err := json.MarshalIndent(listing, "", "  ")
	if err != nil {
		return Result{}, fmt.Errorf("marshal listing manifest: %w", err)
	}
	if err := writeAtomic(filepath.Join(directory, "listing.json"), append(metadata, '\n'), 0o644); err != nil {
		return Result{}, fmt.Errorf("write listing manifest: %w", err)
	}
	listingScript := append([]byte("window.listingArchiveListing = "), metadata...)
	listingScript = append(listingScript, ';', '\n')
	if err := writeAtomic(filepath.Join(directory, "listing.js"), listingScript, 0o644); err != nil {
		return Result{}, fmt.Errorf("write listing manifest script: %w", err)
	}
	if err := writeAtomic(filepath.Join(directory, "README.md"), []byte(renderMarkdown(*listing)), 0o644); err != nil {
		return Result{}, fmt.Errorf("write archive README: %w", err)
	}
	generatedAt := time.Now().UTC()
	if err := writeArchiveManifest(directory, *listing, generatedAt); err != nil {
		return Result{}, err
	}
	if err := updateBrowserIndexes(options.Root, directory, generatedAt); err != nil {
		return Result{}, fmt.Errorf("update browser indexes: %w", err)
	}
	if err := updateBrowserIndexes(options.Root, imagesDir, generatedAt); err != nil {
		return Result{}, fmt.Errorf("update browser indexes: %w", err)
	}
	if len(listing.Videos) > 0 {
		if err := updateBrowserIndexes(options.Root, filepath.Join(directory, "videos"), generatedAt); err != nil {
			return Result{}, fmt.Errorf("update browser indexes: %w", err)
		}
	}
	return Result{
		Directory:        directory,
		Listing:          *listing,
		DiscoveredImages: discoveredImages,
		DiscoveredVideos: discoveredVideos,
		NewAssets:        countAssetsByStatus(listing.Images, listing.Videos, "new"),
		ReusedAssets:     countAssetsByStatus(listing.Images, listing.Videos, "existing"),
		FailedAssets:     countAssetsByStatus(listing.Images, listing.Videos, "failed"),
	}, nil
}

func (s Service) extract(ctx context.Context, rawURL, finalURL string, html []byte) (*model.Listing, site.MediaURLs, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(html)))
	if err != nil {
		return nil, site.MediaURLs{}, fmt.Errorf("parse HTML: %w", err)
	}
	extractor := site.Select(finalURL, s.Extractors...)
	if extractor == nil {
		return nil, site.MediaURLs{}, fmt.Errorf("unsupported listing site: %s", downloader.DisplayURL(finalURL))
	}
	listing, mediaURLs, err := extractor.Extract(ctx, site.Page{URL: finalURL, HTML: html, Doc: doc})
	if err != nil {
		return nil, site.MediaURLs{}, fmt.Errorf("extract listing: %w", err)
	}
	listing.Source.URL = rawURL
	if listing.Source.CanonicalURL == "" {
		listing.Source.CanonicalURL = finalURL
	}
	return listing, mediaURLs, nil
}

func listingDirectory(root string, listing model.Listing) (string, error) {
	address := archiveAddress(listing)
	return pathutil.JoinUnder(root,
		pathutil.Component(firstNonEmpty(listing.Location.Country, "Unknown Country"), "Unknown Country"),
		pathutil.Component(firstNonEmpty(listing.Location.Region, "Unknown State"), "Unknown State"),
		pathutil.Component(firstNonEmpty(listing.Location.Municipality, "Unknown Municipality"), "Unknown Municipality"),
		pathutil.Component(address, "Unknown Address"),
	)
}

func resolveListingDirectory(root string, listing model.Listing) (string, error) {
	directory, err := listingDirectory(root, listing)
	if err != nil {
		return "", err
	}
	if err := ensureSameArchive(directory, listing); err == nil {
		return directory, nil
	} else if !errors.Is(err, ErrArchiveCollision) {
		return "", err
	}

	suffix := listing.Source.ListingID
	if suffix == "" {
		hash := sha256.Sum256([]byte(firstNonEmpty(listing.Source.CanonicalURL, listing.Source.URL)))
		suffix = fmt.Sprintf("%x", hash[:4])
	}
	address := archiveAddress(listing)
	directory, err = pathutil.JoinUnder(root,
		pathutil.Component(firstNonEmpty(listing.Location.Country, "Unknown Country"), "Unknown Country"),
		pathutil.Component(firstNonEmpty(listing.Location.Region, "Unknown State"), "Unknown State"),
		pathutil.Component(firstNonEmpty(listing.Location.Municipality, "Unknown Municipality"), "Unknown Municipality"),
		pathutil.Component(fmt.Sprintf("%s [%s]", address, suffix), "Unknown Address"),
	)
	if err != nil {
		return "", err
	}
	if err := ensureSameArchive(directory, listing); err != nil {
		return "", err
	}
	return directory, nil
}

func archiveAddress(listing model.Listing) string {
	return firstNonEmpty(listing.Location.Street, listing.Location.Address, listing.Property.Title, "Unknown Address")
}

func BuildExistingIndex(root string) (ExistingIndex, error) {
	index := ExistingIndex{byKey: map[string]string{}}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return index, fmt.Errorf("resolve archive root: %w", err)
	}
	info, err := os.Stat(rootAbs)
	if os.IsNotExist(err) {
		return index, nil
	}
	if err != nil {
		return index, fmt.Errorf("inspect archive root: %w", err)
	}
	if !info.IsDir() {
		return index, fmt.Errorf("archive root is not a directory")
	}
	err = filepath.WalkDir(rootAbs, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() || entry.Name() != "listing.json" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read existing manifest %s: %w", path, err)
		}
		var listing model.Listing
		if err := json.Unmarshal(data, &listing); err != nil {
			return fmt.Errorf("parse existing manifest %s: %w", path, err)
		}
		index.add(listing, filepath.Dir(path))
		return nil
	})
	if err != nil {
		return index, fmt.Errorf("walk archive root: %w", err)
	}
	return index, nil
}

func (index ExistingIndex) Find(listing model.Listing) (string, bool) {
	for _, key := range archiveIndexKeys(listing) {
		if directory, ok := index.byKey[key]; ok {
			return directory, true
		}
	}
	return "", false
}

func (index ExistingIndex) add(listing model.Listing, directory string) {
	for _, key := range archiveIndexKeys(listing) {
		if _, exists := index.byKey[key]; !exists {
			index.byKey[key] = directory
		}
	}
}

func MoveExistingArchive(root, existingDirectory string, listing model.Listing) (string, bool, error) {
	desired, err := listingDirectory(root, listing)
	if err != nil {
		return "", false, err
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", false, fmt.Errorf("resolve archive root: %w", err)
	}
	existingAbs, err := filepath.Abs(existingDirectory)
	if err != nil {
		return "", false, fmt.Errorf("resolve existing archive path: %w", err)
	}
	desiredAbs, err := filepath.Abs(desired)
	if err != nil {
		return "", false, fmt.Errorf("resolve desired archive path: %w", err)
	}
	for _, path := range []string{existingAbs, desiredAbs} {
		rel, err := filepath.Rel(rootAbs, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return "", false, fmt.Errorf("archive path escapes root")
		}
	}
	if existingAbs == desiredAbs {
		return desiredAbs, false, nil
	}
	if err := ensureSameArchive(existingAbs, listing); err != nil {
		return "", false, err
	}
	if err := ensureSameArchive(desiredAbs, listing); err != nil {
		return "", false, err
	}
	if err := os.MkdirAll(filepath.Dir(desiredAbs), 0o755); err != nil {
		return "", false, fmt.Errorf("create migrated archive parent: %w", err)
	}
	if err := os.Rename(existingAbs, desiredAbs); err != nil {
		return "", false, fmt.Errorf("move existing archive to address path: %w", err)
	}
	return desiredAbs, true, nil
}

func archiveIndexKeys(listing model.Listing) []string {
	var keys []string
	for _, rawURL := range []string{listing.Source.CanonicalURL, listing.Source.URL} {
		if value := strings.ToLower(strings.TrimSpace(rawURL)); value != "" {
			keys = append(keys, "url:"+value)
		}
	}
	if listing.Source.Site != "" && listing.Source.ListingID != "" {
		keys = append(keys, "id:"+strings.ToLower(strings.TrimSpace(listing.Source.Site))+":"+strings.ToLower(strings.TrimSpace(listing.Source.ListingID)))
	}
	return keys
}

func ensureSameArchive(directory string, listing model.Listing) error {
	data, err := os.ReadFile(filepath.Join(directory, "listing.json"))
	if os.IsNotExist(err) {
		entries, readErr := os.ReadDir(directory)
		if os.IsNotExist(readErr) || len(entries) == 0 {
			return nil
		}
		if readErr != nil {
			return fmt.Errorf("inspect existing archive: %w", readErr)
		}
		source, sourceErr := os.ReadFile(filepath.Join(directory, "source.url"))
		if sourceErr == nil {
			existingURL := strings.TrimSpace(string(source))
			if existingURL == listing.Source.CanonicalURL || existingURL == listing.Source.URL {
				return nil
			}
		}
		return fmt.Errorf("%w: refuse to overwrite non-empty directory without a matching archive manifest at %s", ErrArchiveCollision, directory)
	}
	if err != nil {
		return fmt.Errorf("inspect existing archive: %w", err)
	}
	var existing model.Listing
	if json.Unmarshal(data, &existing) != nil {
		return fmt.Errorf("%w: refuse to overwrite unrelated or invalid archive at %s", ErrArchiveCollision, directory)
	}
	if existing.Source.CanonicalURL != listing.Source.CanonicalURL && existing.Source.URL != listing.Source.URL {
		return fmt.Errorf("%w: refuse to overwrite archive for a different source at %s", ErrArchiveCollision, directory)
	}
	return nil
}

func writeAtomic(filename string, data []byte, mode os.FileMode) error {
	temp, err := os.CreateTemp(filepath.Dir(filename), "."+filepath.Base(filename)+".tmp-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(mode); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, filename)
}

func renderMarkdown(listing model.Listing) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "# %s\n\n", firstNonEmpty(listing.Property.Title, listing.Location.Address, "Archived listing"))
	fmt.Fprintf(&builder, "- Source: %s\n", listing.Source.URL)
	if listing.Source.CanonicalURL != "" && listing.Source.CanonicalURL != listing.Source.URL {
		fmt.Fprintf(&builder, "- Canonical source: %s\n", listing.Source.CanonicalURL)
	}
	writeMarkdownValue(&builder, "Site", listing.Source.Site)
	fmt.Fprintf(&builder, "- Retrieved: %s\n", listing.Source.RetrievedAt.Format("2006-01-02T15:04:05Z07:00"))
	if listing.Location.Address != "" {
		fmt.Fprintf(&builder, "- Address: %s\n", listing.Location.Address)
	}
	if listing.Location.MapURL != "" {
		fmt.Fprintf(&builder, "- Map: %s\n", listing.Location.MapURL)
	}
	if listing.Location.Latitude != nil && listing.Location.Longitude != nil {
		fmt.Fprintf(&builder, "- Coordinates: %s, %s\n", formatNumber(*listing.Location.Latitude), formatNumber(*listing.Location.Longitude))
	}
	writeMarkdownValue(&builder, "Site listing ID", listing.Source.ListingID)
	writeMarkdownValue(&builder, "Listing reference", listing.Source.ListingReference)
	writeMarkdownValue(&builder, "First listed", listing.Source.FirstListed)
	writeMarkdownValue(&builder, "Last updated", listing.Source.LastUpdated)
	successes := 0
	for _, image := range listing.Images {
		if image.File != "" {
			successes++
		}
	}
	fmt.Fprintf(&builder, "- Images downloaded: %d\n", successes)
	videoSuccesses := 0
	for _, video := range listing.Videos {
		if video.File != "" {
			videoSuccesses++
		}
	}
	if len(listing.Videos) > 0 || listing.Property.VideoURL != "" {
		fmt.Fprintf(&builder, "- Videos downloaded: %d\n", videoSuccesses)
	}

	builder.WriteString("\n## Property\n\n")
	writeMarkdownValue(&builder, "Type", listing.Property.Type)
	writeMarkdownValue(&builder, "Availability", listing.Property.Availability)
	writeMarkdownValue(&builder, "Price", moneyDisplay(listing.Property.Price))
	writeMarkdownValue(&builder, "Price per area", unitPriceDisplay(listing.Property.PricePerArea))
	writeMarkdownNumber(&builder, "Bedrooms", listing.Property.Bedrooms)
	writeMarkdownNumber(&builder, "Bathrooms", listing.Property.Bathrooms)
	writeMarkdownNumber(&builder, "Floors", listing.Property.Floors)
	writeMarkdownValue(&builder, "Interior area", measurementDisplay(listing.Property.InteriorArea))
	writeMarkdownValue(&builder, "Lot area", measurementDisplay(listing.Property.LotArea))
	if listing.Property.YearBuilt != nil {
		fmt.Fprintf(&builder, "- Year built: %d\n", *listing.Property.YearBuilt)
	}
	if listing.Property.PhotoCount != nil {
		fmt.Fprintf(&builder, "- Source photo count: %d\n", *listing.Property.PhotoCount)
	}
	writeMarkdownValue(&builder, "Video", listing.Property.VideoURL)

	if listing.Broker.Agent != "" || listing.Broker.Agency != "" {
		builder.WriteString("\n## Listed By\n\n")
		writeMarkdownValue(&builder, "Agent", listing.Broker.Agent)
		writeMarkdownValue(&builder, "Agent profile", listing.Broker.AgentProfileURL)
		writeMarkdownValue(&builder, "Agent license", listing.Broker.AgentLicense)
		writeMarkdownValue(&builder, "Agency", listing.Broker.Agency)
		writeMarkdownValue(&builder, "Agency profile", listing.Broker.AgencyProfileURL)
		writeMarkdownValue(&builder, "Agency address", listing.Broker.AgencyAddress)
	}
	if listing.Property.Description != "" {
		fmt.Fprintf(&builder, "\n## Description\n\n%s\n", listing.Property.Description)
	}
	if len(listing.Property.Features) > 0 {
		builder.WriteString("\n## Features\n\n")
		for _, feature := range listing.Property.Features {
			fmt.Fprintf(&builder, "- %s\n", feature)
		}
	}
	return builder.String()
}

func (s Service) downloadVideoPosters(ctx context.Context, videosDir, referer string, options Options, lookups existingAssetLookups, videos []model.Asset) error {
	for index := range videos {
		if videos[index].PosterSourceURL == "" {
			continue
		}
		results := s.Client.DownloadImages(ctx, []string{videos[index].PosterSourceURL}, videosDir, downloader.ImageOptions{
			Workers: 1, Overwrite: options.Overwrite, Referer: referer, Existing: lookups.Posters,
		})
		if len(results) == 0 {
			continue
		}
		videos[index].PosterFile = results[0].File
		if videos[index].Status == "" {
			videos[index].Status = results[0].Status
		}
		if results[0].Error != "" && videos[index].Error == "" {
			videos[index].Error = fmt.Sprintf("poster %s: %s", videos[index].PosterSourceURL, results[0].Error)
			videos[index].Status = "failed"
		}
	}
	return nil
}

func countAssetsByStatus(images, videos []model.Asset, status string) int {
	total := 0
	for _, asset := range images {
		if asset.Status == status {
			total++
		}
	}
	for _, asset := range videos {
		if asset.Status == status {
			total++
		}
	}
	return total
}

func readListing(filename string) (model.Listing, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return model.Listing{}, err
	}
	var listing model.Listing
	if err := json.Unmarshal(data, &listing); err != nil {
		return model.Listing{}, err
	}
	return listing, nil
}

func writeMarkdownValue(builder *strings.Builder, label, value string) {
	if strings.TrimSpace(value) != "" {
		fmt.Fprintf(builder, "- %s: %s\n", label, strings.TrimSpace(value))
	}
}

func writeMarkdownNumber(builder *strings.Builder, label string, value *float64) {
	if value != nil {
		fmt.Fprintf(builder, "- %s: %s\n", label, formatNumber(*value))
	}
}

func moneyDisplay(value *model.Money) string {
	if value == nil {
		return ""
	}
	if value.Display != "" {
		return value.Display
	}
	if value.Amount != nil {
		return strings.TrimSpace(fmt.Sprintf("%s %s", formatNumber(*value.Amount), value.Currency))
	}
	return ""
}

func unitPriceDisplay(value *model.UnitPrice) string {
	if value == nil {
		return ""
	}
	if value.Display != "" {
		return value.Display
	}
	if value.Amount != nil {
		return strings.TrimSpace(fmt.Sprintf("%s %s per %s", formatNumber(*value.Amount), value.Currency, value.PerUnit))
	}
	return ""
}

func measurementDisplay(value *model.Measurement) string {
	if value == nil {
		return ""
	}
	if value.Display != "" {
		return value.Display
	}
	if value.Value != nil {
		return strings.TrimSpace(fmt.Sprintf("%s %s", formatNumber(*value.Value), value.Unit))
	}
	return ""
}

func formatNumber(value float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", value), "0"), ".")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

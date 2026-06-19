package archive

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"listing-archiver/internal/model"
)

type VerifyReport struct {
	Directories   int
	Listings      int
	FilesVerified int
	Issues        []string
}

type MigrationReport struct {
	Listings           int
	Migrated           int
	Moved              int
	DirectoriesIndexed int
}

func VerifyArchive(root string) (VerifyReport, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return VerifyReport{}, fmt.Errorf("resolve archive root: %w", err)
	}
	info, err := os.Stat(rootAbs)
	if err != nil {
		return VerifyReport{}, fmt.Errorf("inspect archive root: %w", err)
	}
	if !info.IsDir() {
		return VerifyReport{}, fmt.Errorf("archive root is not a directory")
	}
	var report VerifyReport
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
		if !entry.IsDir() {
			return nil
		}
		report.Directories++
		report.Issues = append(report.Issues, verifyBrowserDirectory(rootAbs, path)...)
		listingFile := filepath.Join(path, "listing.json")
		if _, err := os.Stat(listingFile); err == nil {
			report.Listings++
			issues, filesVerified := verifyListingDirectory(path)
			report.Issues = append(report.Issues, issues...)
			report.FilesVerified += filesVerified
		}
		return nil
	})
	if err != nil {
		return VerifyReport{}, fmt.Errorf("walk archive root: %w", err)
	}
	sort.Strings(report.Issues)
	return report, nil
}

func verifyBrowserDirectory(root, directory string) []string {
	var issues []string
	source, err := browserAssets.ReadFile("index.html")
	if err != nil {
		return []string{fmt.Sprintf("%s: read embedded browser page: %v", directory, err)}
	}
	page, err := os.ReadFile(filepath.Join(directory, "index.html"))
	if err != nil {
		issues = append(issues, fmt.Sprintf("%s: missing index.html", directory))
	} else if !bytes.Equal(page, source) {
		issues = append(issues, fmt.Sprintf("%s: stale index.html", directory))
	}
	expected, err := buildBrowserIndex(root, directory, time.Time{})
	if err != nil {
		return append(issues, fmt.Sprintf("%s: build browser index: %v", directory, err))
	}
	actual, err := readBrowserJSONIndexFile(filepath.Join(directory, "index.json"))
	if err != nil {
		issues = append(issues, fmt.Sprintf("%s: %v", directory, err))
	} else if browserIndexShape(expected) != browserIndexShape(actual) {
		issues = append(issues, fmt.Sprintf("%s: stale index.json", directory))
	}
	scriptData, err := os.ReadFile(filepath.Join(directory, "index.js"))
	if err != nil {
		issues = append(issues, fmt.Sprintf("%s: missing index.js", directory))
	} else if _, err := parseBrowserIndexScript(scriptData); err != nil {
		issues = append(issues, fmt.Sprintf("%s: invalid index.js: %v", directory, err))
	}
	listingData, err := os.ReadFile(filepath.Join(directory, "listing.json"))
	if err == nil {
		if !json.Valid(bytes.TrimSpace(listingData)) {
			issues = append(issues, fmt.Sprintf("%s: invalid listing.json", directory))
		}
		if scriptData, err := os.ReadFile(filepath.Join(directory, "listing.js")); err != nil {
			issues = append(issues, fmt.Sprintf("%s: missing listing.js", directory))
		} else if !bytes.Contains(scriptData, []byte("window.listingArchiveListing")) {
			issues = append(issues, fmt.Sprintf("%s: invalid listing.js", directory))
		}
	}
	return issues
}

func verifyListingDirectory(directory string) ([]string, int) {
	var issues []string
	filesVerified := 0
	listing, err := readListing(filepath.Join(directory, "listing.json"))
	if err != nil {
		return []string{fmt.Sprintf("%s: parse listing.json: %v", directory, err)}, 0
	}
	if listing.SchemaVersion != model.SchemaVersion {
		issues = append(issues, fmt.Sprintf("%s: listing schema version %d is older than current %d", directory, listing.SchemaVersion, model.SchemaVersion))
	}
	manifest, err := readArchiveManifest(filepath.Join(directory, "manifest.json"))
	if err != nil {
		return append(issues, fmt.Sprintf("%s: read manifest.json: %v", directory, err)), 0
	}
	currentFiles, err := manifestFiles(directory)
	if err != nil {
		return append(issues, fmt.Sprintf("%s: build current file manifest: %v", directory, err)), 0
	}
	filesVerified += len(currentFiles)
	if !reflect.DeepEqual(manifest.Files, currentFiles) {
		issues = append(issues, fmt.Sprintf("%s: manifest.json file inventory is stale", directory))
	}
	fileSet := map[string]archiveManifestFile{}
	for _, file := range currentFiles {
		fileSet[file.Path] = file
	}
	for _, asset := range manifest.Assets {
		if asset.Path != "" {
			file, ok := fileSet[asset.Path]
			if !ok {
				issues = append(issues, fmt.Sprintf("%s: missing asset file %s", directory, asset.Path))
			} else if asset.Bytes > 0 && file.Bytes != asset.Bytes {
				issues = append(issues, fmt.Sprintf("%s: asset size mismatch for %s", directory, asset.Path))
			}
		}
		if asset.PosterPath != "" {
			if _, ok := fileSet[asset.PosterPath]; !ok {
				issues = append(issues, fmt.Sprintf("%s: missing poster file %s", directory, asset.PosterPath))
			}
		}
	}
	if manifest.Source.URL != listing.Source.URL {
		issues = append(issues, fmt.Sprintf("%s: manifest source URL does not match listing.json", directory))
	}
	if sourceURL, err := os.ReadFile(filepath.Join(directory, "source.url")); err == nil {
		stored := strings.TrimSpace(string(sourceURL))
		if stored != "" && stored != listing.Source.URL && stored != listing.Source.CanonicalURL {
			issues = append(issues, fmt.Sprintf("%s: source.url does not match listing source URLs", directory))
		}
	}
	return issues, filesVerified
}

func MigrateArchive(root string, now time.Time) (MigrationReport, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return MigrationReport{}, fmt.Errorf("resolve archive root: %w", err)
	}
	var listingDirs []string
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
		if entry.IsDir() {
			if _, err := os.Stat(filepath.Join(path, "listing.json")); err == nil {
				listingDirs = append(listingDirs, path)
			}
		}
		return nil
	})
	if err != nil {
		return MigrationReport{}, fmt.Errorf("walk archive root: %w", err)
	}
	report := MigrationReport{Listings: len(listingDirs)}
	for _, dir := range listingDirs {
		listing, err := readListing(filepath.Join(dir, "listing.json"))
		if err != nil {
			return report, fmt.Errorf("read listing manifest %s: %w", dir, err)
		}
		normalized := normalizeListingForCurrentSchema(listing)
		targetDir, moved, err := MoveExistingArchive(rootAbs, dir, normalized)
		if err != nil {
			return report, err
		}
		if moved {
			report.Moved++
		}
		if err := rewriteArchiveListing(targetDir, normalized, now); err != nil {
			return report, err
		}
		report.Migrated++
	}
	count, err := RebuildBrowserIndexes(rootAbs, now.UTC())
	if err != nil {
		return report, err
	}
	report.DirectoriesIndexed = count
	return report, nil
}

func normalizeListingForCurrentSchema(listing model.Listing) model.Listing {
	listing.SchemaVersion = model.SchemaVersion
	if listing.Property.Features == nil {
		listing.Property.Features = []string{}
	}
	if listing.Images == nil {
		listing.Images = []model.Asset{}
	}
	if listing.Videos == nil {
		listing.Videos = []model.Asset{}
	}
	if listing.Warnings == nil {
		listing.Warnings = []string{}
	}
	for index := range listing.Images {
		if listing.Images[index].Status == "" && listing.Images[index].File != "" {
			listing.Images[index].Status = "existing"
		}
	}
	for index := range listing.Videos {
		if listing.Videos[index].Status == "" && listing.Videos[index].File != "" {
			listing.Videos[index].Status = "existing"
		}
	}
	return listing
}

func rewriteArchiveListing(directory string, listing model.Listing, now time.Time) error {
	data, err := json.MarshalIndent(listing, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal listing manifest: %w", err)
	}
	if err := writeAtomic(filepath.Join(directory, "listing.json"), append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write listing manifest: %w", err)
	}
	script := append([]byte("window.listingArchiveListing = "), data...)
	script = append(script, ';', '\n')
	if err := writeAtomic(filepath.Join(directory, "listing.js"), script, 0o644); err != nil {
		return fmt.Errorf("write listing manifest script: %w", err)
	}
	if err := writeAtomic(filepath.Join(directory, "README.md"), []byte(renderMarkdown(listing)), 0o644); err != nil {
		return fmt.Errorf("write archive README: %w", err)
	}
	if err := writeArchiveManifest(directory, listing, now.UTC()); err != nil {
		return err
	}
	return nil
}

func readBrowserJSONIndexFile(filename string) (browserIndex, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return browserIndex{}, fmt.Errorf("read index.json: %w", err)
	}
	var index browserIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return browserIndex{}, fmt.Errorf("parse index.json: %w", err)
	}
	return index, nil
}

func parseBrowserIndexScript(data []byte) (browserIndex, error) {
	prefix := []byte("window." + browserIndexVariable + " = ")
	if !bytes.HasPrefix(data, prefix) || !bytes.HasSuffix(data, []byte(";\n")) {
		return browserIndex{}, fmt.Errorf("unexpected browser index script format")
	}
	var index browserIndex
	if err := json.Unmarshal(data[len(prefix):len(data)-2], &index); err != nil {
		return browserIndex{}, err
	}
	return index, nil
}

func browserIndexShape(index browserIndex) string {
	index.LastUpdated = time.Time{}
	data, _ := json.Marshal(index)
	return string(data)
}

package archive

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

//go:embed index.html assets/*
var browserAssets embed.FS

const browserIndexSchemaVersion = 1
const browserIndexVariable = "listingArchiveIndex"

type browserIndex struct {
	SchemaVersion int                `json:"schema_version"`
	Path          string             `json:"path"`
	Parent        string             `json:"parent,omitempty"`
	Directories   []browserDirectory `json:"directories"`
	Files         []browserFile      `json:"files"`
	LastUpdated   time.Time          `json:"last_updated"`
}

type browserDirectory struct {
	Name string `json:"name"`
	Href string `json:"href"`
}

type browserFile struct {
	Name string `json:"name"`
	Href string `json:"href"`
	Size int64  `json:"size"`
}

func updateBrowserIndexes(root, leaf string, now time.Time) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve browser index root: %w", err)
	}
	leafAbs, err := filepath.Abs(leaf)
	if err != nil {
		return fmt.Errorf("resolve browser index leaf: %w", err)
	}
	rel, err := filepath.Rel(rootAbs, leafAbs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("browser index leaf escapes archive root")
	}

	directories := []string{rootAbs}
	if rel != "." {
		current := rootAbs
		for _, component := range strings.Split(rel, string(filepath.Separator)) {
			current = filepath.Join(current, component)
			directories = append(directories, current)
		}
	}
	for _, directory := range directories {
		if err := writeBrowserIndex(rootAbs, directory, now.UTC()); err != nil {
			return err
		}
	}
	return nil
}

func RebuildBrowserIndexes(root string, now time.Time) (int, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return 0, fmt.Errorf("resolve browser index root: %w", err)
	}
	info, err := os.Stat(rootAbs)
	if err != nil {
		return 0, fmt.Errorf("inspect browser index root: %w", err)
	}
	if !info.IsDir() {
		return 0, fmt.Errorf("browser index root is not a directory")
	}
	var directories []string
	err = filepath.WalkDir(rootAbs, func(path string, entry fs.DirEntry, walkErr error) error {
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
			directories = append(directories, path)
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("walk browser index root: %w", err)
	}
	for _, directory := range directories {
		if err := writeBrowserIndex(rootAbs, directory, now.UTC()); err != nil {
			return 0, err
		}
	}
	return len(directories), nil
}

func writeBrowserIndex(root, directory string, now time.Time) error {
	source, err := browserAssets.ReadFile("index.html")
	if err != nil {
		return fmt.Errorf("read browser index page: %w", err)
	}
	if err := writeAtomic(filepath.Join(directory, "index.html"), source, 0o644); err != nil {
		return fmt.Errorf("write browser index page: %w", err)
	}
	if err := rebuildListingScript(directory); err != nil {
		return err
	}
	if err := writeListingPageAssets(directory); err != nil {
		return err
	}

	index, err := buildBrowserIndex(root, directory, now)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal browser index: %w", err)
	}
	script := append([]byte("window."+browserIndexVariable+" = "), data...)
	script = append(script, ';', '\n')
	if err := writeAtomic(filepath.Join(directory, "index.json"), append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write browser index JSON data: %w", err)
	}
	if err := writeAtomic(filepath.Join(directory, "index.js"), script, 0o644); err != nil {
		return fmt.Errorf("write browser index JavaScript data: %w", err)
	}
	return nil
}

func buildBrowserIndex(root, directory string, now time.Time) (browserIndex, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return browserIndex{}, fmt.Errorf("read browser index directory: %w", err)
	}
	rel, err := filepath.Rel(root, directory)
	if err != nil {
		return browserIndex{}, fmt.Errorf("resolve browser index path: %w", err)
	}
	index := browserIndex{
		SchemaVersion: browserIndexSchemaVersion,
		Path:          filepath.ToSlash(rel),
		Directories:   []browserDirectory{},
		Files:         []browserFile{},
		LastUpdated:   now,
	}
	if rel == "." {
		index.Path = "."
	} else {
		index.Parent = "../index.html"
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == "index.html" || name == "index.js" || name == "index.json" || name == "listing.js" || name == "video.min.js" || name == "video-js.min.css" || strings.HasPrefix(name, ".index.") {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		if entry.IsDir() {
			index.Directories = append(index.Directories, browserDirectory{Name: name, Href: pathHref(name) + "/index.html"})
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return browserIndex{}, fmt.Errorf("inspect browser index entry %q: %w", name, err)
		}
		index.Files = append(index.Files, browserFile{Name: name, Href: pathHref(name), Size: info.Size()})
	}
	sort.Slice(index.Directories, func(i, j int) bool {
		return strings.ToLower(index.Directories[i].Name) < strings.ToLower(index.Directories[j].Name)
	})
	sort.Slice(index.Files, func(i, j int) bool {
		return strings.ToLower(index.Files[i].Name) < strings.ToLower(index.Files[j].Name)
	})
	return index, nil
}

func rebuildListingScript(directory string) error {
	data, err := os.ReadFile(filepath.Join(directory, "listing.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read listing manifest for browser page: %w", err)
	}
	data = []byte(strings.TrimSpace(string(data)))
	if !json.Valid(data) {
		return fmt.Errorf("listing manifest is invalid JSON: %s", filepath.Join(directory, "listing.json"))
	}
	script := append([]byte("window.listingArchiveListing = "), data...)
	script = append(script, ';', '\n')
	if err := writeAtomic(filepath.Join(directory, "listing.js"), script, 0o644); err != nil {
		return fmt.Errorf("write listing manifest script: %w", err)
	}
	return nil
}

func writeListingPageAssets(directory string) error {
	if _, err := os.Stat(filepath.Join(directory, "listing.json")); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect listing manifest for browser assets: %w", err)
	}
	for _, asset := range []string{"assets/video.min.js", "assets/video-js.min.css"} {
		data, err := browserAssets.ReadFile(asset)
		if err != nil {
			return fmt.Errorf("read embedded browser asset %q: %w", asset, err)
		}
		if err := writeAtomic(filepath.Join(directory, filepath.Base(asset)), data, 0o644); err != nil {
			return fmt.Errorf("write embedded browser asset %q: %w", asset, err)
		}
	}
	return nil
}

func pathHref(name string) string {
	return (&url.URL{Path: name}).EscapedPath()
}

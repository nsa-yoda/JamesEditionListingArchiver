package archive

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"listing-archiver/internal/model"
)

const archiveManifestSchemaVersion = 1

type archiveManifest struct {
	SchemaVersion int                    `json:"schema_version"`
	GeneratedAt   time.Time              `json:"generated_at"`
	Source        model.Source           `json:"source"`
	Files         []archiveManifestFile  `json:"files"`
	Assets        []archiveManifestAsset `json:"assets"`
	Summary       archiveManifestSummary `json:"summary"`
}

type archiveManifestFile struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

type archiveManifestAsset struct {
	Kind       string `json:"kind"`
	SourceURL  string `json:"source_url"`
	Path       string `json:"path,omitempty"`
	MediaType  string `json:"media_type,omitempty"`
	Bytes      int64  `json:"bytes,omitempty"`
	Status     string `json:"status,omitempty"`
	PosterURL  string `json:"poster_url,omitempty"`
	PosterPath string `json:"poster_path,omitempty"`
	Error      string `json:"error,omitempty"`
}

type archiveManifestSummary struct {
	ImagesNew      int `json:"images_new"`
	ImagesExisting int `json:"images_existing"`
	ImagesFailed   int `json:"images_failed"`
	VideosNew      int `json:"videos_new"`
	VideosExisting int `json:"videos_existing"`
	VideosFailed   int `json:"videos_failed"`
}

type existingAssetLookups struct {
	Images  map[string]model.Asset
	Videos  map[string]model.Asset
	Posters map[string]model.Asset
}

func loadExistingAssetLookups(directory string) (existingAssetLookups, error) {
	if manifest, err := readArchiveManifest(filepath.Join(directory, "manifest.json")); err == nil {
		lookups := existingAssetLookups{
			Images:  map[string]model.Asset{},
			Videos:  map[string]model.Asset{},
			Posters: map[string]model.Asset{},
		}
		for _, asset := range manifest.Assets {
			if asset.SourceURL == "" || asset.Path == "" {
				continue
			}
			file := filepath.Base(filepath.FromSlash(asset.Path))
			value := model.Asset{
				SourceURL: asset.SourceURL,
				File:      file,
				MediaType: asset.MediaType,
				Bytes:     asset.Bytes,
				Status:    asset.Status,
				Error:     asset.Error,
			}
			switch asset.Kind {
			case "image":
				lookups.Images[asset.SourceURL] = value
			case "video":
				value.PosterSourceURL = asset.PosterURL
				if asset.PosterPath != "" {
					value.PosterFile = filepath.Base(filepath.FromSlash(asset.PosterPath))
				}
				lookups.Videos[asset.SourceURL] = value
				if value.PosterSourceURL != "" && value.PosterFile != "" {
					lookups.Posters[value.PosterSourceURL] = model.Asset{
						SourceURL: value.PosterSourceURL,
						File:      value.PosterFile,
						Status:    "existing",
					}
				}
			}
		}
		return lookups, nil
	} else if !os.IsNotExist(err) {
		return existingAssetLookups{}, err
	}

	data, err := os.ReadFile(filepath.Join(directory, "listing.json"))
	if os.IsNotExist(err) {
		return existingAssetLookups{
			Images:  map[string]model.Asset{},
			Videos:  map[string]model.Asset{},
			Posters: map[string]model.Asset{},
		}, nil
	}
	if err != nil {
		return existingAssetLookups{}, fmt.Errorf("read listing manifest for existing assets: %w", err)
	}
	var listing model.Listing
	if err := json.Unmarshal(data, &listing); err != nil {
		return existingAssetLookups{}, fmt.Errorf("parse listing manifest for existing assets: %w", err)
	}
	lookups := existingAssetLookups{
		Images:  make(map[string]model.Asset, len(listing.Images)),
		Videos:  make(map[string]model.Asset, len(listing.Videos)),
		Posters: map[string]model.Asset{},
	}
	for _, asset := range listing.Images {
		if asset.SourceURL != "" {
			lookups.Images[asset.SourceURL] = asset
		}
	}
	for _, asset := range listing.Videos {
		if asset.SourceURL != "" {
			lookups.Videos[asset.SourceURL] = asset
		}
		if asset.PosterSourceURL != "" && asset.PosterFile != "" {
			lookups.Posters[asset.PosterSourceURL] = model.Asset{
				SourceURL: asset.PosterSourceURL,
				File:      asset.PosterFile,
				Status:    "existing",
			}
		}
	}
	return lookups, nil
}

func writeArchiveManifest(directory string, listing model.Listing, generatedAt time.Time) error {
	manifest, err := buildArchiveManifest(directory, listing, generatedAt)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal archive manifest: %w", err)
	}
	if err := writeAtomic(filepath.Join(directory, "manifest.json"), append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write archive manifest: %w", err)
	}
	return nil
}

func buildArchiveManifest(directory string, listing model.Listing, generatedAt time.Time) (archiveManifest, error) {
	files, err := manifestFiles(directory)
	if err != nil {
		return archiveManifest{}, err
	}
	assets := make([]archiveManifestAsset, 0, len(listing.Images)+len(listing.Videos))
	for _, asset := range listing.Videos {
		assets = append(assets, archiveManifestAssetFromModel("video", "videos", asset))
	}
	for _, asset := range listing.Images {
		assets = append(assets, archiveManifestAssetFromModel("image", "images", asset))
	}
	return archiveManifest{
		SchemaVersion: archiveManifestSchemaVersion,
		GeneratedAt:   generatedAt.UTC(),
		Source:        listing.Source,
		Files:         files,
		Assets:        assets,
		Summary:       summarizeManifestAssets(assets),
	}, nil
}

func manifestFiles(directory string) ([]archiveManifestFile, error) {
	var files []archiveManifestFile
	err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, walkErr error) error {
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
			return nil
		}
		name := entry.Name()
		if name == "manifest.json" || name == "index.html" || name == "index.json" || name == "index.js" || name == "listing.js" || name == "video.min.js" || name == "video-js.min.css" || strings.HasPrefix(name, ".index.") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect archive manifest entry %q: %w", path, err)
		}
		rel, err := filepath.Rel(directory, path)
		if err != nil {
			return fmt.Errorf("resolve archive manifest path: %w", err)
		}
		hash, err := fileSHA256(path)
		if err != nil {
			return fmt.Errorf("hash archive manifest entry %q: %w", path, err)
		}
		files = append(files, archiveManifestFile{
			Path:   filepath.ToSlash(rel),
			Bytes:  info.Size(),
			SHA256: hash,
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("build archive manifest file list: %w", err)
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].Path < files[j].Path
	})
	return files, nil
}

func archiveManifestAssetFromModel(kind, subdir string, asset model.Asset) archiveManifestAsset {
	path := ""
	if asset.File != "" {
		path = filepath.ToSlash(filepath.Join(subdir, asset.File))
	}
	posterPath := ""
	if asset.PosterFile != "" {
		posterPath = filepath.ToSlash(filepath.Join(subdir, asset.PosterFile))
	}
	return archiveManifestAsset{
		Kind:       kind,
		SourceURL:  asset.SourceURL,
		Path:       path,
		MediaType:  asset.MediaType,
		Bytes:      asset.Bytes,
		Status:     asset.Status,
		PosterURL:  asset.PosterSourceURL,
		PosterPath: posterPath,
		Error:      asset.Error,
	}
}

func readArchiveManifest(filename string) (archiveManifest, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return archiveManifest{}, err
	}
	var manifest archiveManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return archiveManifest{}, fmt.Errorf("parse archive manifest: %w", err)
	}
	return manifest, nil
}

func summarizeManifestAssets(assets []archiveManifestAsset) archiveManifestSummary {
	var summary archiveManifestSummary
	for _, asset := range assets {
		switch asset.Kind {
		case "video":
			switch asset.Status {
			case "existing":
				summary.VideosExisting++
			case "failed":
				summary.VideosFailed++
			default:
				summary.VideosNew++
			}
		default:
			switch asset.Status {
			case "existing":
				summary.ImagesExisting++
			case "failed":
				summary.ImagesFailed++
			default:
				summary.ImagesNew++
			}
		}
	}
	return summary
}

func fileSHA256(filename string) (string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

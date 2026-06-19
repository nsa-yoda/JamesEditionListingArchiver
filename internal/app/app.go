package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"listing-archiver/internal/archive"
	"listing-archiver/internal/config"
	"listing-archiver/internal/downloader"
	"listing-archiver/internal/site"
	"listing-archiver/internal/site/jamesedition"
)

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return RunWithInput(ctx, args, nil, stdout, stderr)
}

func RunWithInput(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	cfg, err := config.Parse(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}
	var urls []string
	if len(cfg.URLs) > 0 || cfg.InputFile != "" {
		urls, err = config.ResolveURLs(cfg, stdin)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
	}
	completed := 0
	var imports []config.HTMLImport
	if cfg.HTMLFile != "" || cfg.HTMLDir != "" {
		imports, err = config.ResolveHTMLImports(cfg)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
	}
	var client *downloader.Client
	if len(urls) > 0 || (len(imports) > 0 && !cfg.MetadataOnly) {
		client, err = downloader.NewClient(downloader.ClientConfig{
			Timeout: cfg.Timeout, UserAgent: cfg.UserAgent, CookiesFile: cfg.CookiesFile,
		})
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
	} else if len(imports) > 0 {
		client = &downloader.Client{}
	}
	service := archive.Service{
		Client:     client,
		Extractors: []site.Extractor{jamesedition.Extractor{}},
	}
	options := archive.Options{
		Root: cfg.Root, Workers: cfg.Workers, Overwrite: cfg.Overwrite,
		MetadataOnly: cfg.MetadataOnly, MaxImages: cfg.MaxImages, AssetsOnly: cfg.AssetsOnly,
	}
	existing := archive.ExistingIndex{}
	if len(imports) > 0 || cfg.AssetsOnly {
		existing, err = archive.BuildExistingIndex(cfg.Root)
		if err != nil {
			fmt.Fprintf(stderr, "error: inspect existing archive: %v\n", err)
			return 1
		}
	}
	if len(urls) > 0 {
		for _, rawURL := range urls {
			if err := ctx.Err(); err != nil {
				fmt.Fprintf(stderr, "error: %v\n", err)
				break
			}
			result, err := service.Run(ctx, rawURL, options)
			if err != nil {
				fmt.Fprintf(stderr, "error: %s: %v\n", downloader.DisplayURL(rawURL), err)
				continue
			}
			successes := 0
			for _, image := range result.Listing.Images {
				if image.File != "" {
					successes++
				}
			}
			for _, video := range result.Listing.Videos {
				if video.File != "" {
					successes++
				}
			}
			fmt.Fprintf(stdout, "Archived listing to: %s\n", result.Directory)
			fmt.Fprintf(stdout, "Discovered %d image URL(s) and %d video URL(s); downloaded %d file(s) (%d new, %d reused, %d failed).\n", result.DiscoveredImages, result.DiscoveredVideos, successes, result.NewAssets, result.ReusedAssets, result.FailedAssets)
			completed++
		}
	}
	imported := 0
	skipped := 0
	importFailures := 0
	migrated := 0
	for _, item := range imports {
		if item.Err != nil {
			fmt.Fprintf(stderr, "error: import %s: %v\n", item.Filename, item.Err)
			importFailures++
			continue
		}
		listing, _, inspectErr := service.InspectHTML(ctx, item.SourceURL, item.HTML)
		if inspectErr == nil {
			if directory, ok := existing.Find(listing); ok {
				if !cfg.Refresh && !cfg.AssetsOnly {
					fmt.Fprintf(stdout, "Skipped existing: %s [found: %s]\n", item.Filename, archivePageReference(cfg.Root, directory))
					skipped++
					continue
				}
				if !cfg.AssetsOnly {
					movedTo, moved, err := archive.MoveExistingArchive(cfg.Root, directory, listing)
					if err != nil {
						fmt.Fprintf(stderr, "error: import %s: %v\n", item.Filename, err)
						importFailures++
						continue
					}
					if moved {
						fmt.Fprintf(stdout, "Moved existing archive: %s -> %s\n", archivePageReference(cfg.Root, directory), archivePageReference(cfg.Root, movedTo))
						migrated++
					}
				}
			}
		}
		result, err := service.ImportHTML(ctx, item.SourceURL, item.HTML, options)
		if err != nil {
			fmt.Fprintf(stderr, "error: import %s: %v\n", item.Filename, err)
			importFailures++
			continue
		}
		successes := 0
		for _, image := range result.Listing.Images {
			if image.File != "" {
				successes++
			}
		}
		for _, video := range result.Listing.Videos {
			if video.File != "" {
				successes++
			}
		}
		fmt.Fprintf(stdout, "Imported HTML archive to: %s\n", result.Directory)
		fmt.Fprintf(stdout, "Discovered %d image URL(s) and %d video URL(s); downloaded %d file(s) (%d new, %d reused, %d failed).\n", result.DiscoveredImages, result.DiscoveredVideos, successes, result.NewAssets, result.ReusedAssets, result.FailedAssets)
		imported++
	}
	if len(urls) > 1 {
		fmt.Fprintf(stdout, "Completed %d/%d listings; %d failed.\n", completed, len(urls), len(urls)-completed)
	}
	if len(imports) > 1 {
		fmt.Fprintf(stdout, "Imported %d/%d HTML files; %d skipped; %d failed.\n", imported, len(imports), skipped, importFailures)
	}
	if migrated > 0 {
		count, err := archive.RebuildBrowserIndexes(cfg.Root, time.Now().UTC())
		if err != nil {
			fmt.Fprintf(stderr, "error: rebuild browser indexes after archive migration: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "Rebuilt browser indexes in %d directories after moving %d archive(s).\n", count, migrated)
	}
	if cfg.Reindex {
		count, err := archive.RebuildBrowserIndexes(cfg.Root, time.Now().UTC())
		if err != nil {
			fmt.Fprintf(stderr, "error: rebuild browser indexes: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "Rebuilt browser indexes in %d directories beneath: %s\n", count, cfg.Root)
	}
	if cfg.Migrate {
		report, err := archive.MigrateArchive(cfg.Root, time.Now().UTC())
		if err != nil {
			fmt.Fprintf(stderr, "error: migrate archive: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "Migrated %d archive(s); moved %d archive(s); rebuilt browser indexes in %d directories.\n", report.Migrated, report.Moved, report.DirectoriesIndexed)
	}
	if cfg.Verify {
		report, err := archive.VerifyArchive(cfg.Root)
		if err != nil {
			fmt.Fprintf(stderr, "error: verify archive: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "Verified %d directories, %d listing archive(s), and %d file entries.\n", report.Directories, report.Listings, report.FilesVerified)
		if len(report.Issues) > 0 {
			for _, issue := range report.Issues {
				fmt.Fprintf(stderr, "verify: %s\n", issue)
			}
			return 1
		}
	}
	if completed != len(urls) || importFailures > 0 {
		return 1
	}
	return 0
}

func archivePageReference(root, directory string) string {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return filepath.ToSlash(filepath.Join(directory, "index.html"))
	}
	directoryAbs, err := filepath.Abs(directory)
	if err != nil {
		return filepath.ToSlash(filepath.Join(directory, "index.html"))
	}
	rel, err := filepath.Rel(rootAbs, directoryAbs)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return filepath.ToSlash(filepath.Join(directoryAbs, "index.html"))
	}
	return "/" + filepath.ToSlash(filepath.Join(rel, "index.html"))
}

package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"listing-archiver/internal/downloader"
)

const MaxImportedHTMLBytes = 32 << 20

type HTMLImport struct {
	Filename  string
	SourceURL string
	MediaDir  string
	HTML      []byte
	Err       error
}

func ResolveHTMLImports(cfg Config) ([]HTMLImport, error) {
	if cfg.HTMLFile != "" {
		html, err := readHTMLFile(cfg.HTMLFile)
		if err != nil {
			return nil, err
		}
		if err := downloader.SafeURL(cfg.SourceURL); err != nil {
			return nil, fmt.Errorf("invalid -source-url: %w", err)
		}
		return []HTMLImport{{Filename: cfg.HTMLFile, SourceURL: cfg.SourceURL, MediaDir: sidecarMediaDir(cfg.HTMLFile), HTML: html}}, nil
	}
	if cfg.HTMLDir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(cfg.HTMLDir)
	if err != nil {
		return nil, fmt.Errorf("read HTML import directory: %w", err)
	}
	var filenames []string
	for _, entry := range entries {
		extension := strings.ToLower(filepath.Ext(entry.Name()))
		if entry.Type()&os.ModeSymlink == 0 && !entry.IsDir() && (extension == ".html" || extension == ".htm") {
			filenames = append(filenames, filepath.Join(cfg.HTMLDir, entry.Name()))
		}
	}
	sort.Slice(filenames, func(i, j int) bool {
		return strings.ToLower(filenames[i]) < strings.ToLower(filenames[j])
	})
	if len(filenames) == 0 {
		return nil, fmt.Errorf("HTML import directory contains no .html files")
	}
	imports := make([]HTMLImport, 0, len(filenames))
	for _, filename := range filenames {
		html, err := readHTMLFile(filename)
		if err != nil {
			imports = append(imports, HTMLImport{Filename: filename, Err: err})
			continue
		}
		sourceURL, err := discoverSourceURL(filename, html)
		if err != nil {
			imports = append(imports, HTMLImport{Filename: filename, HTML: html, Err: err})
			continue
		}
		imports = append(imports, HTMLImport{Filename: filename, SourceURL: sourceURL, MediaDir: sidecarMediaDir(filename), HTML: html})
	}
	return imports, nil
}

func sidecarMediaDir(filename string) string {
	return strings.TrimSuffix(filename, filepath.Ext(filename)) + "_files"
}

func readHTMLFile(filename string) ([]byte, error) {
	info, err := os.Lstat(filename)
	if err != nil {
		return nil, fmt.Errorf("inspect imported HTML %q: %w", filename, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("imported HTML %q is not a regular file", filename)
	}
	if info.Size() > MaxImportedHTMLBytes {
		return nil, fmt.Errorf("imported HTML %q exceeds %d bytes", filename, MaxImportedHTMLBytes)
	}
	html, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("read imported HTML %q: %w", filename, err)
	}
	if len(html) == 0 {
		return nil, fmt.Errorf("imported HTML %q is empty", filename)
	}
	return html, nil
}

func discoverSourceURL(filename string, html []byte) (string, error) {
	sidecar := strings.TrimSuffix(filename, filepath.Ext(filename)) + ".url"
	if info, err := os.Lstat(sidecar); err == nil && info.Mode().IsRegular() && info.Size() <= 16*1024 {
		if value, err := os.ReadFile(sidecar); err == nil {
			for _, rawURL := range sidecarURLs(string(value)) {
				if downloader.SafeURL(rawURL) == nil {
					return rawURL, nil
				}
			}
		}
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(html)))
	if err != nil {
		return "", fmt.Errorf("parse imported HTML %q: %w", filename, err)
	}
	candidates := []string{}
	if value, ok := doc.Find(`link[rel="canonical"]`).First().Attr("href"); ok {
		candidates = append(candidates, value)
	}
	if value, ok := doc.Find(`meta[property="og:url"]`).First().Attr("content"); ok {
		candidates = append(candidates, value)
	}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		u, err := url.Parse(candidate)
		if err == nil && u.IsAbs() && downloader.SafeURL(candidate) == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no usable source URL found for imported HTML %q; add an absolute canonical/og:url or same-basename .url file", filename)
}

func sidecarURLs(value string) []string {
	out := []string{strings.TrimSpace(value)}
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if len(line) > 4 && strings.EqualFold(line[:4], "URL=") {
			out = append(out, strings.TrimSpace(line[4:]))
		}
	}
	return out
}

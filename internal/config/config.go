package config

import (
	"flag"
	"fmt"
	"io"
	"strings"
	"time"
)

const DefaultUserAgent = "listing-archiver/1.0 (+personal archival)"

type Config struct {
	URLs         []string
	InputFile    string
	Root         string
	CookiesFile  string
	Workers      int
	Timeout      time.Duration
	UserAgent    string
	Overwrite    bool
	MetadataOnly bool
	MaxImages    int
	Refresh      bool
	SkipExisting bool
	Reindex      bool
	Verify       bool
	Migrate      bool
	AssetsOnly   bool
	VideosOnly   bool
	HTMLFile     string
	HTMLDir      string
	SourceURL    string
}

func Parse(args []string, stderr io.Writer) (Config, error) {
	var cfg Config
	var urls stringList
	fs := flag.NewFlagSet("listing-archiver", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Var(&urls, "url", "listing URL to archive; may be repeated")
	fs.StringVar(&cfg.InputFile, "input", "", "newline-delimited URL file, or - for standard input")
	fs.StringVar(&cfg.Root, "root", "./listings", "archive root directory")
	fs.StringVar(&cfg.CookiesFile, "cookies", "", "JSON or Netscape-format cookie export")
	fs.IntVar(&cfg.Workers, "workers", 6, "concurrent image downloads")
	fs.DurationVar(&cfg.Timeout, "timeout", 45*time.Second, "per-request timeout")
	fs.StringVar(&cfg.UserAgent, "user-agent", DefaultUserAgent, "HTTP User-Agent")
	fs.BoolVar(&cfg.Overwrite, "overwrite", false, "replace already-downloaded image files")
	fs.BoolVar(&cfg.MetadataOnly, "metadata-only", false, "archive metadata and source HTML without downloading images")
	fs.IntVar(&cfg.MaxImages, "max-images", 0, "maximum images per listing; 0 means unlimited")
	fs.BoolVar(&cfg.Refresh, "refresh", false, "deprecated; HTML imports are reprocessed by default")
	fs.BoolVar(&cfg.SkipExisting, "skip-existing", false, "skip HTML imports whose listing already exists")
	fs.BoolVar(&cfg.Reindex, "reindex", false, "rebuild browser indexes beneath the archive root")
	fs.BoolVar(&cfg.Verify, "verify", false, "verify archive manifests, file hashes, and browser indexes beneath the archive root")
	fs.BoolVar(&cfg.Migrate, "migrate", false, "migrate existing archives to the current schema and regenerate manifests/indexes")
	fs.BoolVar(&cfg.AssetsOnly, "assets-only", false, "refresh only images/videos for an existing archive while preserving current listing metadata")
	fs.BoolVar(&cfg.VideosOnly, "videos-only", false, "refresh only videos for existing HTML imports while preserving metadata and images")
	fs.StringVar(&cfg.HTMLFile, "html", "", "import saved listing HTML instead of fetching the page")
	fs.StringVar(&cfg.HTMLDir, "html-dir", "", "import top-level .html and .htm files in a directory")
	fs.StringVar(&cfg.SourceURL, "source-url", "", "original listing URL for -html import")
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	cfg.URLs = append(urls, fs.Args()...)
	if cfg.Workers < 1 {
		return Config{}, fmt.Errorf("-workers must be at least 1")
	}
	if cfg.Timeout <= 0 {
		return Config{}, fmt.Errorf("-timeout must be positive")
	}
	if cfg.MaxImages < 0 {
		return Config{}, fmt.Errorf("-max-images must not be negative")
	}
	if cfg.HTMLFile != "" && cfg.HTMLDir != "" {
		return Config{}, fmt.Errorf("-html and -html-dir are mutually exclusive")
	}
	if cfg.HTMLFile != "" {
		if cfg.SourceURL == "" {
			return Config{}, fmt.Errorf("-source-url is required with -html")
		}
		if len(cfg.URLs) > 0 || cfg.InputFile != "" {
			return Config{}, fmt.Errorf("-html cannot be combined with -url, positional URLs, or -input")
		}
	} else if cfg.SourceURL != "" {
		return Config{}, fmt.Errorf("-source-url requires -html")
	}
	if cfg.HTMLDir != "" && (len(cfg.URLs) > 0 || cfg.InputFile != "") {
		return Config{}, fmt.Errorf("-html-dir cannot be combined with -url, positional URLs, or -input")
	}
	if cfg.AssetsOnly && cfg.MetadataOnly {
		return Config{}, fmt.Errorf("-assets-only cannot be combined with -metadata-only")
	}
	if cfg.VideosOnly && (cfg.AssetsOnly || cfg.MetadataOnly || cfg.Reindex || cfg.Verify || cfg.Migrate) {
		return Config{}, fmt.Errorf("-videos-only cannot be combined with -assets-only, -metadata-only, -reindex, -verify, or -migrate")
	}
	if cfg.AssetsOnly && cfg.Reindex {
		return Config{}, fmt.Errorf("-assets-only cannot be combined with -reindex")
	}
	if cfg.AssetsOnly && cfg.Verify {
		return Config{}, fmt.Errorf("-assets-only cannot be combined with -verify")
	}
	if cfg.AssetsOnly && cfg.Migrate {
		return Config{}, fmt.Errorf("-assets-only cannot be combined with -migrate")
	}
	if len(cfg.URLs) == 0 && cfg.InputFile == "" && cfg.HTMLFile == "" && cfg.HTMLDir == "" && !cfg.Reindex && !cfg.Verify && !cfg.Migrate {
		return Config{}, fmt.Errorf("at least one URL, -input, -html, -html-dir, -reindex, -verify, or -migrate is required")
	}
	return cfg, nil
}

type stringList []string

func (values *stringList) String() string { return strings.Join(*values, ",") }
func (values *stringList) Set(value string) error {
	*values = append(*values, value)
	return nil
}

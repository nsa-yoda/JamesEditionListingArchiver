package config

import (
	"io"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	cfg, err := Parse([]string{"-url", "https://one.test", "-url", "https://two.test", "-workers", "2", "-timeout", "3s", "-refresh", "https://three.test"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Workers != 2 || cfg.Timeout != 3*time.Second || !cfg.Refresh || len(cfg.URLs) != 3 {
		t.Fatalf("unexpected config: %#v", cfg)
	}
}

func TestParseValidation(t *testing.T) {
	for _, args := range [][]string{{}, {"-url", "x", "-workers", "0"}, {"-url", "x", "-timeout", "0s"}, {"-url", "x", "-max-images", "-1"}} {
		if _, err := Parse(args, io.Discard); err == nil {
			t.Fatalf("expected error for %#v", args)
		}
	}
	if cfg, err := Parse([]string{"-reindex"}, io.Discard); err != nil || !cfg.Reindex {
		t.Fatalf("reindex config = %#v, %v", cfg, err)
	}
	if cfg, err := Parse([]string{"-verify"}, io.Discard); err != nil || !cfg.Verify {
		t.Fatalf("verify config = %#v, %v", cfg, err)
	}
	if cfg, err := Parse([]string{"-migrate"}, io.Discard); err != nil || !cfg.Migrate {
		t.Fatalf("migrate config = %#v, %v", cfg, err)
	}
	for _, args := range [][]string{
		{"-assets-only", "-metadata-only", "-url", "https://example.test"},
		{"-assets-only", "-verify"},
		{"-assets-only", "-migrate"},
	} {
		if _, err := Parse(args, io.Discard); err == nil {
			t.Fatalf("expected error for %#v", args)
		}
	}
}

func TestParseHTMLImport(t *testing.T) {
	cfg, err := Parse([]string{"-html", "listing.html", "-source-url", "https://www.jamesedition.com/real_estate/example"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTMLFile != "listing.html" || cfg.SourceURL == "" {
		t.Fatalf("config = %#v", cfg)
	}
	for _, args := range [][]string{
		{"-html", "listing.html"},
		{"-source-url", "https://example.test"},
		{"-html", "listing.html", "-source-url", "https://example.test", "-url", "https://example.test"},
		{"-html", "listing.html", "-html-dir", "imports", "-source-url", "https://example.test"},
		{"-html-dir", "imports", "-url", "https://example.test"},
	} {
		if _, err := Parse(args, io.Discard); err == nil {
			t.Fatalf("expected error for %#v", args)
		}
	}
}

func TestParseVideosOnly(t *testing.T) {
	cfg, err := Parse([]string{"-html-dir", "imports", "-videos-only"}, io.Discard)
	if err != nil || !cfg.VideosOnly {
		t.Fatalf("config = %#v, error = %v", cfg, err)
	}
	for _, args := range [][]string{
		{"-html-dir", "imports", "-videos-only", "-assets-only"},
		{"-html-dir", "imports", "-videos-only", "-metadata-only"},
	} {
		if _, err := Parse(args, io.Discard); err == nil {
			t.Fatalf("expected conflict for %#v", args)
		}
	}
}

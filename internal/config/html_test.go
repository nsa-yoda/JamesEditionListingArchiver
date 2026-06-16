package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveSingleHTMLImport(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "listing.html")
	if err := os.WriteFile(filename, []byte("<html>saved</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	imports, err := ResolveHTMLImports(Config{HTMLFile: filename, SourceURL: "https://www.jamesedition.com/real_estate/example"})
	if err != nil {
		t.Fatal(err)
	}
	if len(imports) != 1 || imports[0].SourceURL == "" || string(imports[0].HTML) != "<html>saved</html>" {
		t.Fatalf("imports = %#v", imports)
	}
}

func TestResolveHTMLDirectoryUsesSidecarAndCanonical(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "b.html"), []byte(`<link rel="canonical" href="https://www.jamesedition.com/real_estate/b">`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.html"), []byte(`<html></html>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.url"), []byte("https://www.jamesedition.com/real_estate/a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "c.html"), []byte(`<html></html>`), 0o644); err != nil {
		t.Fatal(err)
	}
	imports, err := ResolveHTMLImports(Config{HTMLDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(imports) != 3 || !strings.HasSuffix(imports[0].Filename, "a.html") || !strings.HasSuffix(imports[1].Filename, "b.html") {
		t.Fatalf("imports = %#v", imports)
	}
	if imports[0].SourceURL == "" || imports[1].SourceURL == "" || imports[2].Err == nil {
		t.Fatalf("imports = %#v", imports)
	}
}

func TestResolveHTMLDirectorySkipsSymlinks(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.html")
	if err := os.WriteFile(outside, []byte(`<link rel="canonical" href="https://www.jamesedition.com/real_estate/outside">`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "outside.html")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := ResolveHTMLImports(Config{HTMLDir: dir}); err == nil {
		t.Fatal("expected no HTML files error")
	}
}

func TestDiscoverSourceURLSupportsInternetShortcutSidecar(t *testing.T) {
	dir := t.TempDir()
	filename := filepath.Join(dir, "listing.html")
	if err := os.WriteFile(filename, []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "listing.url"), []byte("[InternetShortcut]\r\nURL=https://www.jamesedition.com/real_estate/example\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := discoverSourceURL(filename, []byte("<html></html>"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://www.jamesedition.com/real_estate/example" {
		t.Fatalf("source URL = %q", got)
	}
}

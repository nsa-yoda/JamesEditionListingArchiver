package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestResolveURLsCombinesAndDeduplicatesInputs(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "urls.txt")
	if err := os.WriteFile(filename, []byte("# listings\nhttps://two.test\n\nhttps://three.test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveURLs(Config{URLs: []string{"https://one.test", "https://two.test"}, InputFile: filename}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"https://one.test", "https://two.test", "https://three.test"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestResolveURLsFromStdin(t *testing.T) {
	got, err := ResolveURLs(Config{InputFile: "-"}, strings.NewReader("https://one.test\nhttps://two.test\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %#v", got)
	}
}

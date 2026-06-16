package pathutil

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestComponent(t *testing.T) {
	tests := map[string]string{
		"../bad/name": "-bad-name",
		"CON":         "_CON",
		" trailing. ": "trailing",
		"a\x00b\nc":   "ab c",
		"":            "fallback",
	}
	for input, want := range tests {
		if got := Component(input, "fallback"); got != want {
			t.Errorf("Component(%q) = %q, want %q", input, got, want)
		}
	}
	long := Component(strings.Repeat("é", 100), "")
	if len([]byte(long)) > 160 {
		t.Fatalf("component is %d bytes", len([]byte(long)))
	}
}

func TestJoinUnder(t *testing.T) {
	root := t.TempDir()
	got, err := JoinUnder(root, "country", "address")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, filepath.Clean(root)+string(filepath.Separator)) {
		t.Fatalf("%q is outside %q", got, root)
	}
	if _, err := JoinUnder(root, "..", "escape"); err == nil {
		t.Fatal("expected traversal error")
	}
}

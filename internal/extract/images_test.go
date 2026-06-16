package extract

import (
	"reflect"
	"testing"
)

func TestHighestSrcset(t *testing.T) {
	got := HighestSrcset("small.jpg 320w, large.jpg 1600w, medium.jpg 800w")
	if got != "large.jpg" {
		t.Fatalf("got %q", got)
	}
}

func TestNormalizeAndDedupe(t *testing.T) {
	got := NormalizeAndDedupe("https://example.test/listing/page", []string{"/a.jpg#x", "https://example.test/a.jpg", "data:x"})
	want := []string{"https://example.test/a.jpg"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestDetectImage(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\nrest")
	mediaType, ext, ok := DetectImage("", png, "https://example.test/image")
	if !ok || mediaType != "image/png" || ext != ".png" {
		t.Fatalf("got %q %q %v", mediaType, ext, ok)
	}
	if _, _, ok := DetectImage("text/html", []byte("<html>challenge"), "https://example.test/a.jpg"); ok {
		t.Fatal("accepted HTML as image")
	}
}

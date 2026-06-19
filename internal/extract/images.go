package extract

import (
	"bytes"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type SrcsetCandidate struct {
	URL        string
	Descriptor string
	Score      float64
}

func ParseSrcset(value string) []SrcsetCandidate {
	var out []SrcsetCandidate
	for _, raw := range strings.Split(value, ",") {
		fields := strings.Fields(strings.TrimSpace(raw))
		if len(fields) == 0 {
			continue
		}
		candidate := SrcsetCandidate{URL: fields[0], Score: 1}
		if len(fields) > 1 {
			candidate.Descriptor = fields[1]
			n, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSuffix(fields[1], "w"), "x"), 64)
			if err == nil {
				candidate.Score = n
			}
		}
		out = append(out, candidate)
	}
	return out
}

func HighestSrcset(value string) string {
	candidates := ParseSrcset(value)
	if len(candidates) == 0 {
		return ""
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].Score > candidates[j].Score })
	return candidates[0].URL
}

func NormalizeURL(baseURL, raw string) (string, error) {
	raw = strings.TrimSpace(strings.Trim(raw, `"'`))
	raw = strings.ReplaceAll(raw, `\/`, `/`)
	raw = strings.ReplaceAll(raw, `\u0026`, `&`)
	if raw == "" || strings.HasPrefix(raw, "data:") || strings.HasPrefix(raw, "blob:") {
		return "", fmt.Errorf("unsupported URL")
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	u = base.ResolveReference(u)
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("unsupported URL scheme")
	}
	u.Fragment = ""
	return u.String(), nil
}

func NormalizeAndDedupe(baseURL string, values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		normalized, err := NormalizeURL(baseURL, value)
		if err == nil && !seen[normalized] {
			seen[normalized] = true
			out = append(out, normalized)
		}
	}
	return out
}

func DetectImage(contentType string, header []byte, rawURL string) (mediaType, extension string, ok bool) {
	detected := http.DetectContentType(header)
	declared, _, _ := mime.ParseMediaType(contentType)
	mediaType = detected
	if strings.HasPrefix(declared, "image/") && (strings.HasPrefix(detected, "image/") || declared == "image/svg+xml") {
		mediaType = declared
	}
	extensions := map[string]string{
		"image/jpeg": ".jpg", "image/png": ".png", "image/gif": ".gif",
		"image/webp": ".webp", "image/avif": ".avif", "image/svg+xml": ".svg",
	}
	if ext := extensions[mediaType]; ext != "" {
		return mediaType, ext, true
	}
	if bytes.HasPrefix(header, []byte("\x00\x00\x00")) && bytes.Contains(header[:min(len(header), 32)], []byte("avif")) {
		return "image/avif", ".avif", true
	}
	if u, err := url.Parse(rawURL); err == nil && strings.HasPrefix(declared, "image/") {
		ext := strings.ToLower(filepath.Ext(u.Path))
		if ext != "" {
			return declared, ext, true
		}
	}
	return mediaType, "", false
}

func DetectVideo(contentType string, header []byte, rawURL string) (mediaType, extension string, ok bool) {
	detected := http.DetectContentType(header)
	declared, _, _ := mime.ParseMediaType(contentType)
	mediaType = detected
	if strings.HasPrefix(declared, "video/") {
		mediaType = declared
	}
	extensions := map[string]string{
		"video/mp4":  ".mp4",
		"video/webm": ".webm",
	}
	if ext := extensions[mediaType]; ext != "" {
		return mediaType, ext, true
	}
	if len(header) >= 12 && bytes.Equal(header[4:8], []byte("ftyp")) {
		return "video/mp4", ".mp4", true
	}
	if len(header) >= 4 && bytes.Equal(header[:4], []byte{0x1a, 0x45, 0xdf, 0xa3}) {
		return "video/webm", ".webm", true
	}
	if u, err := url.Parse(rawURL); err == nil {
		switch ext := strings.ToLower(filepath.Ext(u.Path)); ext {
		case ".mp4":
			return "video/mp4", ext, true
		case ".webm":
			return "video/webm", ext, true
		}
	}
	return mediaType, "", false
}

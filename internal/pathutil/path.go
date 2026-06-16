package pathutil

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

var windowsReserved = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

func Component(value, fallback string) string {
	value = strings.Join(strings.FieldsFunc(value, unicode.IsSpace), " ")
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return '-'
		}
		return r
	}, value)
	value = strings.Trim(value, ". ")
	for len([]byte(value)) > 160 {
		_, size := utf8.DecodeLastRuneInString(value)
		value = value[:len(value)-size]
	}
	if value == "" || value == "." || value == ".." {
		value = fallback
	}
	base := strings.ToUpper(strings.TrimSuffix(value, filepath.Ext(value)))
	if windowsReserved[base] {
		value = "_" + value
	}
	if value == "" {
		return "Unknown"
	}
	return value
}

func JoinUnder(root string, components ...string) (string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve archive root: %w", err)
	}
	path := filepath.Join(append([]string{rootAbs}, components...)...)
	rel, err := filepath.Rel(rootAbs, path)
	if err != nil {
		return "", fmt.Errorf("resolve archive path: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("archive path escapes root")
	}
	return path, nil
}

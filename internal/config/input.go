package config

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

func ResolveURLs(cfg Config, stdin io.Reader) ([]string, error) {
	values := append([]string(nil), cfg.URLs...)
	if cfg.InputFile != "" {
		var reader io.Reader
		var file *os.File
		if cfg.InputFile == "-" {
			if stdin == nil {
				return nil, fmt.Errorf("standard input is unavailable")
			}
			reader = stdin
		} else {
			var err error
			file, err = os.Open(cfg.InputFile)
			if err != nil {
				return nil, fmt.Errorf("open input file: %w", err)
			}
			defer file.Close()
			reader = file
		}
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
		for scanner.Scan() {
			value := strings.TrimSpace(scanner.Text())
			if value != "" && !strings.HasPrefix(value, "#") {
				values = append(values, value)
			}
		}
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("read input URLs: %w", err)
		}
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no URLs were provided")
	}
	return out, nil
}

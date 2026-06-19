package downloader

import (
	"bufio"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"listing-archiver/internal/extract"
	"listing-archiver/internal/model"
)

const maxImageBytes = 100 << 20

type AssetKind string

const (
	AssetKindImage AssetKind = "image"
	AssetKindVideo AssetKind = "video"
)

type ImageOptions struct {
	Workers   int
	Overwrite bool
	Referer   string
	Existing  map[string]model.Asset
}

type assetOptions struct {
	ImageOptions
	Kind AssetKind
}

type assetJob struct {
	index int
	url   string
}

func (c *Client) DownloadImages(ctx context.Context, urls []string, directory string, options ImageOptions) []model.Asset {
	return c.downloadAssets(ctx, urls, directory, assetOptions{ImageOptions: options, Kind: AssetKindImage})
}

func (c *Client) DownloadVideos(ctx context.Context, urls []string, directory string, options ImageOptions) []model.Asset {
	return c.downloadAssets(ctx, urls, directory, assetOptions{ImageOptions: options, Kind: AssetKindVideo})
}

func (c *Client) downloadAssets(ctx context.Context, urls []string, directory string, options assetOptions) []model.Asset {
	if options.Workers < 1 {
		options.Workers = 1
	}
	jobs := make(chan assetJob)
	results := make(chan indexedAsset)
	var workers sync.WaitGroup
	for i := 0; i < options.Workers; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for job := range jobs {
				results <- indexedAsset{index: job.index, asset: c.downloadAsset(ctx, job, directory, options)}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for index, rawURL := range urls {
			select {
			case jobs <- assetJob{index: index + 1, url: rawURL}:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		workers.Wait()
		close(results)
	}()
	var collected []indexedAsset
	for result := range results {
		collected = append(collected, result)
	}
	sort.Slice(collected, func(i, j int) bool { return collected[i].index < collected[j].index })
	out := make([]model.Asset, 0, len(collected))
	for _, result := range collected {
		out = append(out, result.asset)
	}
	return out
}

type indexedAsset struct {
	index int
	asset model.Asset
}

func (c *Client) downloadAsset(ctx context.Context, job assetJob, directory string, options assetOptions) model.Asset {
	result := model.Asset{SourceURL: job.url}
	if !options.Overwrite {
		if reused, ok := reuseExistingAsset(directory, job.url, options); ok {
			return reused
		}
	}

	hash := sha256.Sum256([]byte(job.url))
	base := fmt.Sprintf("%s-%x", options.Kind.filePrefix(), hash[:6])
	part := filepath.Join(directory, base+".part")
	offset := int64(0)
	if info, err := os.Stat(part); err == nil {
		offset = info.Size()
		if offset > maxImageBytes {
			_ = os.Remove(part)
			result.Error = fmt.Sprintf("partial %s exceeds size limit", options.Kind)
			result.Status = "failed"
			return result
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, job.url, nil)
	if err != nil {
		result.Error = err.Error()
		result.Status = "failed"
		return result
	}
	setHeaders(req, c.UserAgent, options.Kind.acceptHeader())
	req.Header.Set("Referer", DisplayURL(options.Referer))
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		result.Error = err.Error()
		result.Status = "failed"
		return result
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		result.Error = fmt.Sprintf("HTTP %s", resp.Status)
		result.Status = "failed"
		return result
	}
	if resp.StatusCode != http.StatusPartialContent {
		offset = 0
	} else if !strings.HasPrefix(resp.Header.Get("Content-Range"), fmt.Sprintf("bytes %d-", offset)) {
		_ = os.Remove(part)
		result.Error = fmt.Sprintf("invalid Content-Range for resumed %s", options.Kind)
		result.Status = "failed"
		return result
	}

	reader := bufio.NewReader(resp.Body)
	header, err := reader.Peek(512)
	if err != nil && err != io.EOF && err != bufio.ErrBufferFull {
		result.Error = fmt.Sprintf("read %s header: %v", options.Kind, err)
		result.Status = "failed"
		return result
	}
	if offset > 0 {
		partial, readErr := os.Open(part)
		if readErr != nil {
			result.Error = readErr.Error()
			result.Status = "failed"
			return result
		}
		existingHeader := make([]byte, 512)
		n, readErr := partial.Read(existingHeader)
		partial.Close()
		if readErr != nil && readErr != io.EOF {
			result.Error = readErr.Error()
			result.Status = "failed"
			return result
		}
		header = existingHeader[:n]
	}
	mediaType, extension, ok := options.Kind.detect(resp.Header.Get("Content-Type"), header, job.url)
	if !ok {
		result.Error = fmt.Sprintf("response is not a recognized %s", options.Kind)
		result.Status = "failed"
		return result
	}
	flags := os.O_CREATE | os.O_WRONLY
	if offset > 0 {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	file, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		result.Error = err.Error()
		result.Status = "failed"
		return result
	}
	written, copyErr := io.Copy(file, io.LimitReader(reader, maxImageBytes-offset+1))
	closeErr := file.Close()
	if copyErr != nil {
		result.Error = copyErr.Error()
		result.Status = "failed"
		return result
	}
	if closeErr != nil {
		result.Error = closeErr.Error()
		result.Status = "failed"
		return result
	}
	if offset+written > maxImageBytes {
		_ = os.Remove(part)
		result.Error = fmt.Sprintf("%s exceeds size limit", options.Kind)
		result.Status = "failed"
		return result
	}
	target := filepath.Join(directory, base+extension)
	if err := os.Rename(part, target); err != nil {
		result.Error = err.Error()
		result.Status = "failed"
		return result
	}
	result.File = filepath.Base(target)
	result.MediaType = mediaType
	result.Bytes = offset + written
	result.Status = "new"
	return result
}

func reuseExistingAsset(directory, rawURL string, options assetOptions) (model.Asset, bool) {
	if existing, ok := options.Existing[rawURL]; ok && existing.File != "" {
		if mediaType, size, valid := validateStoredAsset(filepath.Join(directory, existing.File), options.Kind); valid {
			return model.Asset{
				SourceURL: rawURL,
				File:      existing.File,
				MediaType: firstNonEmpty(existing.MediaType, mediaType),
				Bytes:     size,
				Status:    "existing",
			}, true
		}
	}
	hash := sha256.Sum256([]byte(rawURL))
	patterns := []string{
		filepath.Join(directory, fmt.Sprintf("%s-%x.*", options.Kind.filePrefix(), hash[:6])),
		filepath.Join(directory, fmt.Sprintf("*-%x.*", hash[:4])),
	}
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(pattern)
		for _, match := range matches {
			if strings.HasSuffix(match, ".part") {
				continue
			}
			if mediaType, size, valid := validateStoredAsset(match, options.Kind); valid {
				return model.Asset{
					SourceURL: rawURL,
					File:      filepath.Base(match),
					MediaType: mediaType,
					Bytes:     size,
					Status:    "existing",
				}, true
			}
		}
	}
	return model.Asset{}, false
}

func validateStoredAsset(path string, kind AssetKind) (string, int64, bool) {
	info, err := os.Stat(path)
	if err != nil || info.Size() <= 0 {
		return "", 0, false
	}
	file, err := os.Open(path)
	if err != nil {
		return "", 0, false
	}
	defer file.Close()
	header := make([]byte, 512)
	n, err := file.Read(header)
	if err != nil && err != io.EOF {
		return "", 0, false
	}
	mediaType, _, ok := kind.detect("", header[:n], path)
	return mediaType, info.Size(), ok
}

func (kind AssetKind) acceptHeader() string {
	switch kind {
	case AssetKindVideo:
		return "video/webm,video/mp4,video/*,*/*;q=0.8"
	default:
		return "image/avif,image/webp,image/*,*/*;q=0.8"
	}
}

func (kind AssetKind) detect(contentType string, header []byte, rawURL string) (string, string, bool) {
	switch kind {
	case AssetKindVideo:
		return extract.DetectVideo(contentType, header, rawURL)
	default:
		return extract.DetectImage(contentType, header, rawURL)
	}
}

func (kind AssetKind) filePrefix() string {
	switch kind {
	case AssetKindVideo:
		return "vid"
	default:
		return "img"
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

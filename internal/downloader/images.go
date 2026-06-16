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

type ImageOptions struct {
	Workers   int
	Overwrite bool
	Referer   string
}

type imageJob struct {
	index int
	url   string
}

func (c *Client) DownloadImages(ctx context.Context, urls []string, directory string, options ImageOptions) []model.Image {
	if options.Workers < 1 {
		options.Workers = 1
	}
	jobs := make(chan imageJob)
	results := make(chan indexedImage)
	var workers sync.WaitGroup
	for i := 0; i < options.Workers; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for job := range jobs {
				results <- indexedImage{index: job.index, image: c.downloadImage(ctx, job, directory, options)}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for index, rawURL := range urls {
			select {
			case jobs <- imageJob{index: index + 1, url: rawURL}:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		workers.Wait()
		close(results)
	}()
	var collected []indexedImage
	for result := range results {
		collected = append(collected, result)
	}
	sort.Slice(collected, func(i, j int) bool { return collected[i].index < collected[j].index })
	out := make([]model.Image, 0, len(collected))
	for _, result := range collected {
		out = append(out, result.image)
	}
	return out
}

type indexedImage struct {
	index int
	image model.Image
}

func (c *Client) downloadImage(ctx context.Context, job imageJob, directory string, options ImageOptions) model.Image {
	result := model.Image{SourceURL: job.url}
	hash := sha256.Sum256([]byte(job.url))
	base := fmt.Sprintf("%03d-%x", job.index, hash[:4])
	if !options.Overwrite {
		matches, _ := filepath.Glob(filepath.Join(directory, base+".*"))
		for _, match := range matches {
			if strings.HasSuffix(match, ".part") {
				continue
			}
			if info, err := os.Stat(match); err == nil && info.Size() > 0 {
				file, openErr := os.Open(match)
				if openErr != nil {
					continue
				}
				header := make([]byte, 512)
				n, readErr := file.Read(header)
				file.Close()
				if readErr != nil && readErr != io.EOF {
					continue
				}
				mediaType, _, valid := extract.DetectImage("", header[:n], match)
				if valid {
					result.File = filepath.Base(match)
					result.MediaType = mediaType
					result.Bytes = info.Size()
					return result
				}
			}
		}
	}

	part := filepath.Join(directory, base+".part")
	offset := int64(0)
	if info, err := os.Stat(part); err == nil {
		offset = info.Size()
		if offset > maxImageBytes {
			_ = os.Remove(part)
			result.Error = "partial image exceeds size limit"
			return result
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, job.url, nil)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	setHeaders(req, c.UserAgent, "image/avif,image/webp,image/*,*/*;q=0.8")
	req.Header.Set("Referer", DisplayURL(options.Referer))
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		result.Error = fmt.Sprintf("HTTP %s", resp.Status)
		return result
	}
	if resp.StatusCode != http.StatusPartialContent {
		offset = 0
	} else if !strings.HasPrefix(resp.Header.Get("Content-Range"), fmt.Sprintf("bytes %d-", offset)) {
		_ = os.Remove(part)
		result.Error = "invalid Content-Range for resumed image"
		return result
	}

	reader := bufio.NewReader(resp.Body)
	header, err := reader.Peek(512)
	if err != nil && err != io.EOF && err != bufio.ErrBufferFull {
		result.Error = fmt.Sprintf("read image header: %v", err)
		return result
	}
	if offset > 0 {
		partial, readErr := os.Open(part)
		if readErr != nil {
			result.Error = readErr.Error()
			return result
		}
		existingHeader := make([]byte, 512)
		n, readErr := partial.Read(existingHeader)
		partial.Close()
		if readErr != nil && readErr != io.EOF {
			result.Error = readErr.Error()
			return result
		}
		header = existingHeader[:n]
	}
	mediaType, extension, ok := extract.DetectImage(resp.Header.Get("Content-Type"), header, job.url)
	if !ok {
		result.Error = "response is not a recognized image"
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
		return result
	}
	written, copyErr := io.Copy(file, io.LimitReader(reader, maxImageBytes-offset+1))
	closeErr := file.Close()
	if copyErr != nil {
		result.Error = copyErr.Error()
		return result
	}
	if closeErr != nil {
		result.Error = closeErr.Error()
		return result
	}
	if offset+written > maxImageBytes {
		_ = os.Remove(part)
		result.Error = "image exceeds size limit"
		return result
	}
	target := filepath.Join(directory, base+extension)
	if err := os.Rename(part, target); err != nil {
		result.Error = err.Error()
		return result
	}
	result.File = filepath.Base(target)
	result.MediaType = mediaType
	result.Bytes = offset + written
	return result
}

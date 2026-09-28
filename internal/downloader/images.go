package downloader

import (
	"bufio"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"listing-archiver/internal/extract"
	"listing-archiver/internal/model"
)

const maxImageBytes = 100 << 20
const maxVideoBytes = 2 << 30

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
	LocalDir  string
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
	var maxBytes int64 = maxImageBytes
	if options.Kind == AssetKindVideo {
		maxBytes = maxVideoBytes
	}
	if !options.Overwrite {
		if reused, ok := reuseExistingAsset(directory, job.url, options); ok {
			return reused
		}
		if imported, ok := importLocalAsset(directory, job.url, options); ok {
			return imported
		}
	}
	if options.Kind == AssetKindVideo && isSupportedVideoPlayerURL(job.url) {
		videoCtx := ctx
		cancel := func() {}
		if c.HTTP != nil && c.HTTP.Timeout > 0 {
			videoCtx, cancel = context.WithTimeout(ctx, c.HTTP.Timeout)
		}
		defer cancel()
		return downloadWithYtDlp(videoCtx, directory, job.url, options.Referer)
	}

	hash := sha256.Sum256([]byte(job.url))
	base := fmt.Sprintf("%s-%x", options.Kind.filePrefix(), hash[:6])
	part := filepath.Join(directory, base+".part")
	offset := int64(0)
	if info, err := os.Stat(part); err == nil {
		offset = info.Size()
		if offset > maxBytes {
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
	written, copyErr := io.Copy(file, io.LimitReader(reader, maxBytes-offset+1))
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
	if offset+written > maxBytes {
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

func isSupportedVideoPlayerURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Hostname()) {
	case "players.brightcove.net":
		return u.Query().Get("videoId") != ""
	case "player.vimeo.com":
		return strings.HasPrefix(u.Path, "/video/")
	case "vimeo.com", "www.vimeo.com":
		return u.Path != "/" && u.Path != ""
	case "youtube.com", "www.youtube.com", "m.youtube.com":
		return u.Query().Get("v") != "" || strings.HasPrefix(u.Path, "/embed/") || strings.HasPrefix(u.Path, "/shorts/")
	case "youtu.be":
		return len(strings.Trim(u.Path, "/")) > 0
	default:
		return false
	}
}

func downloadWithYtDlp(ctx context.Context, directory, rawURL, referer string) model.Asset {
	result := model.Asset{SourceURL: rawURL}
	ytDlp, err := exec.LookPath("yt-dlp")
	if err != nil {
		result.Status = "failed"
		result.Error = "video player URL requires yt-dlp, which was not found in PATH"
		return result
	}
	hash := sha256.Sum256([]byte(rawURL))
	base := fmt.Sprintf(".brightcove-%x", hash[:6])
	template := filepath.Join(directory, base+".%%(ext)s")
	args := []string{
		"--no-playlist", "--no-progress", "--no-warnings", "--no-part", "--max-filesize", "2G",
		"--format", "best[ext=mp4]/best", "--merge-output-format", "mp4",
		"--output", template,
	}
	if referer != "" {
		args = append(args, "--referer", referer)
	}
	args = append(args, rawURL)
	command := exec.CommandContext(ctx, ytDlp, args...)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		for _, path := range globVideoOutputs(directory, base) {
			_ = os.Remove(path)
		}
		result.Status = "failed"
		result.Error = "yt-dlp could not download the video player URL"
		return result
	}
	for _, path := range globVideoOutputs(directory, base) {
		info, err := os.Stat(path)
		if err != nil || info.Size() == 0 || info.Size() > maxVideoBytes {
			_ = os.Remove(path)
			continue
		}
		file, err := os.Open(path)
		if err != nil {
			_ = os.Remove(path)
			continue
		}
		header := make([]byte, 512)
		n, readErr := file.Read(header)
		_ = file.Close()
		if readErr != nil && readErr != io.EOF {
			_ = os.Remove(path)
			continue
		}
		mediaType, extension, ok := extract.DetectVideo("", header[:n], path)
		if !ok || (extension != ".mp4" && extension != ".webm") {
			_ = os.Remove(path)
			continue
		}
		target := filepath.Join(directory, fmt.Sprintf("vid-%x%s", hash[:6], extension))
		if err := os.Rename(path, target); err != nil {
			_ = os.Remove(path)
			continue
		}
		result.File = filepath.Base(target)
		result.MediaType = mediaType
		result.Bytes = info.Size()
		result.Status = "new"
		return result
	}
	result.Status = "failed"
	result.Error = "yt-dlp produced no recognized local MP4 or WebM file"
	return result
}

func globVideoOutputs(directory, base string) []string {
	paths, _ := filepath.Glob(filepath.Join(directory, base+".*"))
	return paths
}

func importLocalAsset(directory, rawURL string, options assetOptions) (model.Asset, bool) {
	if options.LocalDir == "" {
		return model.Asset{}, false
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return model.Asset{}, false
	}
	name, err := url.PathUnescape(filepath.Base(u.Path))
	if err != nil || name == "." || name == ".." || name == string(filepath.Separator) || name == "" || strings.ContainsAny(name, `/\\`) {
		return model.Asset{}, false
	}
	rootInfo, err := os.Lstat(options.LocalDir)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return model.Asset{}, false
	}
	path := filepath.Join(options.LocalDir, name)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		alternate, ok := jamesEditionThumbnailVariant(u, name)
		if !ok {
			return model.Asset{}, false
		}
		path = filepath.Join(options.LocalDir, alternate)
		info, err = os.Lstat(path)
	}
	var maxBytes int64 = maxImageBytes
	if options.Kind == AssetKindVideo {
		maxBytes = maxVideoBytes
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxBytes {
		return model.Asset{}, false
	}
	source, err := os.Open(path)
	if err != nil {
		return model.Asset{}, false
	}
	defer source.Close()
	header := make([]byte, 512)
	n, err := source.Read(header)
	if err != nil && err != io.EOF {
		return model.Asset{}, false
	}
	mediaType, extension, ok := options.Kind.detect(mime.TypeByExtension(filepath.Ext(name)), header[:n], rawURL)
	if !ok {
		return model.Asset{}, false
	}
	if digest, err := hashFile(source); err == nil {
		if existing, ok := findIdenticalAsset(directory, options.Kind, digest); ok {
			return model.Asset{SourceURL: rawURL, File: existing.File, MediaType: existing.MediaType, Bytes: existing.Bytes, Status: "existing"}, true
		}
	}
	hash := sha256.Sum256([]byte(rawURL))
	target := filepath.Join(directory, fmt.Sprintf("%s-%x%s", options.Kind.filePrefix(), hash[:6], extension))
	if _, _, valid := validateStoredAsset(target, options.Kind); !valid {
		if _, err := source.Seek(0, io.SeekStart); err != nil {
			return model.Asset{}, false
		}
		temporary := target + ".part"
		output, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			return model.Asset{}, false
		}
		written, copyErr := io.Copy(output, io.LimitReader(source, maxBytes+1))
		closeErr := output.Close()
		if copyErr != nil || closeErr != nil || written != info.Size() || written > maxBytes {
			_ = os.Remove(temporary)
			return model.Asset{}, false
		}
		if err := os.Rename(temporary, target); err != nil {
			_ = os.Remove(temporary)
			return model.Asset{}, false
		}
	}
	return model.Asset{SourceURL: rawURL, File: filepath.Base(target), MediaType: mediaType, Bytes: info.Size(), Status: "new"}, true
}

func jamesEditionThumbnailVariant(source *url.URL, name string) (string, bool) {
	if !strings.EqualFold(source.Hostname(), "www.jamesedition.com") || !strings.Contains(source.Path, "_files/") || !strings.HasPrefix(name, "1100xxs") || !strings.HasSuffix(strings.ToLower(name), ".jpg") {
		return "", false
	}
	suffix := strings.TrimSuffix(strings.TrimPrefix(name, "1100xxs"), ".jpg")
	if suffix != "" {
		if len(suffix) < 3 || suffix[0] != '(' || suffix[len(suffix)-1] != ')' {
			return "", false
		}
		for _, digit := range suffix[1 : len(suffix)-1] {
			if digit < '0' || digit > '9' {
				return "", false
			}
		}
	}
	return "2200xxsxm" + suffix + ".jpg", true
}

func findIdenticalAsset(directory string, kind AssetKind, digest [32]byte) (model.Asset, bool) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return model.Asset{}, false
	}
	for _, entry := range entries {
		if entry.IsDir() || strings.HasSuffix(entry.Name(), ".part") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		if _, _, ok := validateStoredAsset(path, kind); !ok {
			continue
		}
		candidate, err := hashFileAtPath(path)
		if err != nil || candidate != digest {
			continue
		}
		mediaType, size, _ := validateStoredAsset(path, kind)
		return model.Asset{File: entry.Name(), MediaType: mediaType, Bytes: size, Status: "existing"}, true
	}
	return model.Asset{}, false
}

func hashFile(file *os.File) ([32]byte, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return [32]byte{}, err
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return [32]byte{}, err
	}
	var digest [32]byte
	copy(digest[:], hash.Sum(nil))
	_, err := file.Seek(0, io.SeekStart)
	return digest, err
}

func hashFileAtPath(path string) ([32]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return [32]byte{}, err
	}
	defer file.Close()
	return hashFile(file)
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

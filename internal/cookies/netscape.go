package cookies

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Report struct {
	Loaded    int
	RawScoped int
	Skipped   int
}

type RawCookie struct {
	Name                          string
	Value                         string
	Domain                        string
	Path                          string
	HostOnly                      bool
	Secure                        bool
	Expires                       time.Time
	Partitioned                   bool
	PartitionTopLevelSite         string
	PartitionHasCrossSiteAncestor bool
}

type File struct {
	Report Report
	Raw    []RawCookie
}

func LoadFile(filename string, jar http.CookieJar) error {
	_, err := LoadFileWithReport(filename, jar)
	return err
}

func LoadFileWithReport(filename string, jar http.CookieJar) (File, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return File{}, err
	}
	return LoadBytes(data, jar)
}

func Load(r io.Reader, jar http.CookieJar) error {
	_, err := LoadWithReport(r, jar)
	return err
}

func LoadWithReport(r io.Reader, jar http.CookieJar) (Report, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return Report{}, err
	}
	file, err := LoadBytes(data, jar)
	return file.Report, err
}

func LoadBytes(data []byte, jar http.CookieJar) (File, error) {
	trimmed := bytes.TrimSpace(bytes.TrimPrefix(data, []byte("\uFEFF")))
	if len(trimmed) > 0 && (trimmed[0] == '[' || trimmed[0] == '{') {
		return loadJSON(trimmed, jar)
	}
	report, err := loadNetscape(bytes.NewReader(data), jar)
	return File{Report: report}, err
}

func loadNetscape(r io.Reader, jar http.CookieJar) (Report, error) {
	var report Report
	grouped := map[string][]*http.Cookie{}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if lineNumber == 1 {
			line = strings.TrimPrefix(line, "\uFEFF")
		}
		httpOnly := false
		if strings.HasPrefix(line, "#HttpOnly_") {
			httpOnly = true
			line = strings.TrimPrefix(line, "#HttpOnly_")
		} else if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) == 6 {
			// Some exporters omit the final tab for cookies with empty values.
			fields = append(fields, "")
		}
		if len(fields) != 7 {
			return report, fmt.Errorf("invalid Netscape cookie record at line %d", lineNumber)
		}
		domain := strings.TrimSpace(fields[0])
		if domain == "" || fields[2] == "" || fields[5] == "" {
			return report, fmt.Errorf("invalid Netscape cookie record at line %d", lineNumber)
		}
		expiresUnix, err := strconv.ParseInt(fields[4], 10, 64)
		if err != nil {
			return report, fmt.Errorf("invalid cookie expiry at line %d", lineNumber)
		}
		secure := strings.EqualFold(fields[3], "TRUE")
		cookie := &http.Cookie{
			Name: fields[5], Value: fields[6], Domain: domain, Path: fields[2],
			Secure: secure, HttpOnly: httpOnly,
		}
		if expiresUnix > 0 {
			cookie.Expires = time.Unix(expiresUnix, 0)
		}
		if err := cookie.Valid(); err != nil {
			// Exporters can include browser-internal values that net/http
			// cannot serialize. Skipping them preserves the remaining cookies
			// without changing the invalid value or exposing it in logs.
			report.Skipped++
			continue
		}
		scheme := "http"
		if secure {
			scheme = "https"
		}
		grouped[scheme+"://"+strings.TrimPrefix(domain, ".")] = append(grouped[scheme+"://"+strings.TrimPrefix(domain, ".")], cookie)
		report.Loaded++
	}
	if err := scanner.Err(); err != nil {
		return report, err
	}
	for rawURL, values := range grouped {
		u, err := url.Parse(rawURL)
		if err != nil {
			return report, fmt.Errorf("parse cookie domain: %w", err)
		}
		jar.SetCookies(u, values)
	}
	return report, nil
}

type jsonCookie struct {
	Domain         string          `json:"domain"`
	ExpirationDate *float64        `json:"expirationDate"`
	HostOnly       bool            `json:"hostOnly"`
	HTTPOnly       bool            `json:"httpOnly"`
	Name           string          `json:"name"`
	PartitionKey   json.RawMessage `json:"partitionKey"`
	Path           string          `json:"path"`
	SameSite       string          `json:"sameSite"`
	Secure         bool            `json:"secure"`
	Session        bool            `json:"session"`
	Value          string          `json:"value"`
}

type jsonPartitionKey struct {
	TopLevelSite         string `json:"topLevelSite"`
	HasCrossSiteAncestor bool   `json:"hasCrossSiteAncestor"`
}

func loadJSON(data []byte, jar http.CookieJar) (File, error) {
	var records []jsonCookie
	if len(data) > 0 && data[0] == '{' {
		var envelope struct {
			Cookies []jsonCookie `json:"cookies"`
		}
		if err := json.Unmarshal(data, &envelope); err != nil {
			return File{}, fmt.Errorf("parse JSON cookie export: %w", err)
		}
		records = envelope.Cookies
		if records == nil {
			return File{}, fmt.Errorf("parse JSON cookie export: object has no cookies array")
		}
	} else if err := json.Unmarshal(data, &records); err != nil {
		return File{}, fmt.Errorf("parse JSON cookie export: %w", err)
	}
	var result File
	grouped := map[string][]*http.Cookie{}
	for index, record := range records {
		var partitionKey *jsonPartitionKey
		if len(record.PartitionKey) > 0 && string(record.PartitionKey) != "null" {
			var parsed jsonPartitionKey
			if err := json.Unmarshal(record.PartitionKey, &parsed); err != nil || parsed.TopLevelSite == "" {
				return result, fmt.Errorf("invalid JSON partition key at index %d", index)
			}
			partitionKey = &parsed
		}
		domain := strings.TrimSpace(record.Domain)
		path := record.Path
		if path == "" {
			path = "/"
		}
		if domain == "" || record.Name == "" || !strings.HasPrefix(path, "/") {
			return result, fmt.Errorf("invalid JSON cookie record at index %d", index)
		}
		cookieDomain := domain
		if record.HostOnly {
			cookieDomain = ""
		}
		cookie := &http.Cookie{
			Name: record.Name, Value: record.Value, Domain: cookieDomain, Path: path,
			Secure: record.Secure, HttpOnly: record.HTTPOnly, SameSite: parseSameSite(record.SameSite),
			Partitioned: partitionKey != nil,
		}
		if !record.Session && record.ExpirationDate != nil && *record.ExpirationDate > 0 {
			cookie.Expires = time.Unix(int64(*record.ExpirationDate), 0)
		}
		scheme := "http"
		if record.Secure {
			scheme = "https"
		}
		rawURL := scheme + "://" + strings.TrimPrefix(domain, ".")
		if err := cookie.Valid(); err != nil {
			if rawCookieValid(record.Name, record.Value) && cookieMetadataValid(cookie) {
				result.Raw = append(result.Raw, rawCookie(record, domain, path, cookie.Expires, partitionKey))
				result.Report.RawScoped++
			} else {
				result.Report.Skipped++
			}
			continue
		}
		if partitionKey != nil {
			// net/http.Cookie validates the Partitioned attribute, but the
			// standard cookiejar does not retain its partition key. Preserve it
			// in the scoped transport instead.
			result.Raw = append(result.Raw, rawCookie(record, domain, path, cookie.Expires, partitionKey))
			result.Report.RawScoped++
			continue
		}
		grouped[rawURL] = append(grouped[rawURL], cookie)
		result.Report.Loaded++
	}
	for rawURL, values := range grouped {
		u, err := url.Parse(rawURL)
		if err != nil {
			return result, fmt.Errorf("parse cookie domain: %w", err)
		}
		jar.SetCookies(u, values)
	}
	return result, nil
}

func cookieMetadataValid(cookie *http.Cookie) bool {
	copy := *cookie
	copy.Value = ""
	return copy.Valid() == nil
}

func rawCookie(record jsonCookie, domain, path string, expires time.Time, partitionKey *jsonPartitionKey) RawCookie {
	raw := RawCookie{
		Name: record.Name, Value: record.Value, Domain: domain, Path: path,
		HostOnly: record.HostOnly, Secure: record.Secure, Expires: expires,
	}
	if partitionKey != nil {
		raw.Partitioned = true
		raw.PartitionTopLevelSite = partitionKey.TopLevelSite
		raw.PartitionHasCrossSiteAncestor = partitionKey.HasCrossSiteAncestor
	}
	return raw
}

func parseSameSite(value string) http.SameSite {
	switch strings.ToLower(value) {
	case "lax":
		return http.SameSiteLaxMode
	case "strict":
		return http.SameSiteStrictMode
	case "none", "no_restriction":
		return http.SameSiteNoneMode
	default:
		return http.SameSiteDefaultMode
	}
}

func rawCookieValid(name, value string) bool {
	if name == "" || strings.ContainsAny(name, " \t\r\n;=") {
		return false
	}
	return !strings.ContainsAny(value, "\r\n;\x00")
}

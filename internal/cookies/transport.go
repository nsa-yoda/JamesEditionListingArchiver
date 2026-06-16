package cookies

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/publicsuffix"
)

func Transport(base http.RoundTripper, raw []RawCookie) http.RoundTripper {
	if len(raw) == 0 {
		return base
	}
	return &rawTransport{base: base, cookies: append([]RawCookie(nil), raw...)}
}

type rawTransport struct {
	base    http.RoundTripper
	cookies []RawCookie
}

func (transport *rawTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	cloned := request.Clone(request.Context())
	cloned.Header = request.Header.Clone()
	header := cloned.Header.Get("Cookie")
	for _, cookie := range transport.cookies {
		if !cookieMatches(cookie, cloned) {
			continue
		}
		if header != "" {
			header += "; "
		}
		header += cookie.Name + "=" + cookie.Value
	}
	if header != "" {
		cloned.Header.Set("Cookie", header)
	}
	return transport.base.RoundTrip(cloned)
}

func cookieMatches(cookie RawCookie, request *http.Request) bool {
	if !cookie.Expires.IsZero() && cookie.Expires.Before(time.Now()) {
		return false
	}
	if cookie.Secure && request.URL.Scheme != "https" {
		return false
	}
	if cookie.Partitioned {
		topLevelURL := request.URL
		if referer := request.Referer(); referer != "" {
			if parsed, err := url.Parse(referer); err == nil && parsed.Scheme != "" && parsed.Hostname() != "" {
				topLevelURL = parsed
			}
		}
		if cookie.PartitionHasCrossSiteAncestor || !sameSchemefulSite(cookie.PartitionTopLevelSite, topLevelURL) {
			return false
		}
	}
	host := strings.ToLower(request.URL.Hostname())
	domain := strings.ToLower(strings.TrimPrefix(cookie.Domain, "."))
	if cookie.HostOnly {
		if host != domain {
			return false
		}
	} else if host != domain && !strings.HasSuffix(host, "."+domain) {
		return false
	}
	requestPath := request.URL.EscapedPath()
	if requestPath == "" {
		requestPath = "/"
	}
	cookiePath := cookie.Path
	if cookiePath == "" {
		cookiePath = "/"
	}
	if requestPath == cookiePath {
		return true
	}
	if !strings.HasPrefix(requestPath, cookiePath) {
		return false
	}
	return strings.HasSuffix(cookiePath, "/") || (len(requestPath) > len(cookiePath) && requestPath[len(cookiePath)] == '/')
}

func sameSchemefulSite(rawSite string, requestURL *url.URL) bool {
	site, err := url.Parse(rawSite)
	if err != nil || site.Scheme == "" || site.Hostname() == "" || site.Scheme != requestURL.Scheme {
		return false
	}
	return registrableDomain(site.Hostname()) == registrableDomain(requestURL.Hostname())
}

func registrableDomain(host string) string {
	host = strings.ToLower(host)
	domain, err := publicsuffix.EffectiveTLDPlusOne(host)
	if err != nil {
		return host
	}
	return domain
}

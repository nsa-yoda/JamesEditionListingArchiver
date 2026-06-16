package cookies

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestLoadJSONBrowserExtensionArray(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	data := `[
	  {"domain":"example.test","hostOnly":true,"httpOnly":true,"name":"standard","path":"/","sameSite":"lax","secure":false,"session":true,"storeId":"0","value":"value"},
	  {"domain":".example.test","hostOnly":false,"httpOnly":false,"name":"raw","path":"/private","sameSite":"no_restriction","secure":true,"session":true,"storeId":"0","value":"quoted,\"value\""}
	]`
	loaded, err := LoadBytes([]byte(data), jar)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Report.Loaded != 1 || loaded.Report.RawScoped != 1 || loaded.Report.Skipped != 0 || len(loaded.Raw) != 1 {
		t.Fatalf("loaded = %#v", loaded)
	}
	u, _ := url.Parse("http://example.test/")
	got := jar.Cookies(u)
	if len(got) != 1 || got[0].Name != "standard" || got[0].Value != "value" {
		t.Fatalf("cookies = %#v", got)
	}
}

func TestLoadJSONWrappedObjectPreservesPartitionedCookie(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	data := `{"cookies":[
	  {"domain":"example.test","hostOnly":true,"name":"standard","path":"/","secure":false,"session":false,"expirationDate":2147483647,"value":"value"},
	  {"domain":"example.test","hostOnly":true,"name":"partitioned","partitionKey":{"topLevelSite":"https://example.test"},"path":"/","secure":true,"session":true,"value":"private"}
	]}`
	loaded, err := LoadBytes([]byte(data), jar)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Report.Loaded != 1 || loaded.Report.RawScoped != 1 || loaded.Report.Skipped != 0 {
		t.Fatalf("loaded = %#v", loaded)
	}
	if len(loaded.Raw) != 1 || !loaded.Raw[0].Partitioned || loaded.Raw[0].PartitionTopLevelSite != "https://example.test" {
		t.Fatalf("raw cookies = %#v", loaded.Raw)
	}
}

func TestLoadJSONRejectsInsecurePartitionedCookie(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	data := `[{"domain":"example.test","hostOnly":true,"name":"partitioned","partitionKey":{"topLevelSite":"https://example.test"},"path":"/","secure":false,"session":true,"value":"private"}]`
	loaded, err := LoadBytes([]byte(data), jar)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Report.Skipped != 1 || loaded.Report.RawScoped != 0 {
		t.Fatalf("loaded = %#v", loaded)
	}
}

func TestLoadJSONRejectsMalformedRecordWithoutValueInError(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	_, err := LoadBytes([]byte(`[{"domain":"","name":"name","path":"/","value":"private-secret"}]`), jar)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "private-secret") {
		t.Fatalf("error exposed cookie value: %v", err)
	}
}

func TestRawCookieTransportScope(t *testing.T) {
	var cookieHeader string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		cookieHeader = request.Header.Get("Cookie")
		io.WriteString(w, "ok")
	}))
	defer server.Close()
	serverURL, _ := url.Parse(server.URL)
	client := server.Client()
	baseTransport := client.Transport
	client.Transport = Transport(baseTransport, []RawCookie{
		{Name: "match", Value: `quoted,"value"`, Domain: serverURL.Hostname(), Path: "/", HostOnly: true, Secure: true},
		{Name: "wrong_path", Value: "no", Domain: serverURL.Hostname(), Path: "/private", HostOnly: true, Secure: true},
		{Name: "wrong_domain", Value: "no", Domain: "example.test", Path: "/", HostOnly: true, Secure: true},
	})
	response, err := client.Get(server.URL + "/listing")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if cookieHeader != `match=quoted,"value"` {
		t.Fatalf("Cookie header = %q", cookieHeader)
	}
}

func TestPartitionedCookieTransportScope(t *testing.T) {
	var cookieHeader string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		cookieHeader = request.Header.Get("Cookie")
		io.WriteString(w, "ok")
	}))
	defer server.Close()
	serverURL, _ := url.Parse(server.URL)
	client := server.Client()
	baseTransport := client.Transport
	client.Transport = Transport(baseTransport, []RawCookie{
		{
			Name: "partitioned", Value: "yes", Domain: serverURL.Hostname(), Path: "/",
			HostOnly: true, Secure: true, Partitioned: true, PartitionTopLevelSite: server.URL,
		},
	})
	request, _ := http.NewRequest(http.MethodGet, server.URL+"/image", nil)
	request.Header.Set("Referer", server.URL+"/listing")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if cookieHeader != "partitioned=yes" {
		t.Fatalf("Cookie header = %q", cookieHeader)
	}

	cookieHeader = ""
	request, _ = http.NewRequest(http.MethodGet, server.URL+"/image", nil)
	request.Header.Set("Referer", "https://example.test/listing")
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if cookieHeader != "" {
		t.Fatalf("partitioned cookie leaked across top-level site: %q", cookieHeader)
	}

	cookieHeader = ""
	client.Transport = Transport(baseTransport, []RawCookie{
		{
			Name: "partitioned", Value: "yes", Domain: serverURL.Hostname(), Path: "/",
			HostOnly: true, Secure: true, Partitioned: true, PartitionTopLevelSite: server.URL,
			PartitionHasCrossSiteAncestor: true,
		},
	})
	request, _ = http.NewRequest(http.MethodGet, server.URL+"/image", nil)
	request.Header.Set("Referer", server.URL+"/listing")
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if cookieHeader != "" {
		t.Fatalf("partitioned cookie ignored cross-site-ancestor context: %q", cookieHeader)
	}
}

package cookies

import (
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	data := "# Netscape HTTP Cookie File\n#HttpOnly_.example.test\tTRUE\t/\tTRUE\t2147483647\tsession\tsecret\n"
	if err := Load(strings.NewReader(data), jar); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse("https://example.test/")
	got := jar.Cookies(u)
	if len(got) != 1 || got[0].Name != "session" || got[0].Value != "secret" {
		t.Fatalf("unexpected cookies: %#v", got)
	}
}

func TestLoadPreservesEmptyValueAndHandlesCRLFAndBOM(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	data := "\uFEFF# Netscape HTTP Cookie File\r\n.example.test\tTRUE\t/\tFALSE\t2147483647\tempty\t\r\n"
	if err := Load(strings.NewReader(data), jar); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse("http://example.test/")
	got := jar.Cookies(u)
	if len(got) != 1 || got[0].Name != "empty" || got[0].Value != "" {
		t.Fatalf("unexpected cookies: %#v", got)
	}
}

func TestLoadAcceptsEmptyValueWithoutFinalDelimiter(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	data := ".example.test\tTRUE\t/\tFALSE\t0\tempty\n"
	if err := Load(strings.NewReader(data), jar); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse("http://example.test/")
	got := jar.Cookies(u)
	if len(got) != 1 || got[0].Name != "empty" || got[0].Value != "" {
		t.Fatalf("unexpected cookies: %#v", got)
	}
}

func TestLoadSkipsValuesNetHTTPCannotSerialize(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	data := strings.Join([]string{
		".example.test\tTRUE\t/\tFALSE\t0\tvalid\tvalue",
		".example.test\tTRUE\t/\tFALSE\t0\tinvalid\tquoted,\"value\"",
	}, "\n")
	report, err := LoadWithReport(strings.NewReader(data), jar)
	if err != nil {
		t.Fatal(err)
	}
	if report.Loaded != 1 || report.Skipped != 1 {
		t.Fatalf("report = %#v", report)
	}
	u, _ := url.Parse("http://example.test/")
	got := jar.Cookies(u)
	if len(got) != 1 || got[0].Name != "valid" || got[0].Value != "value" {
		t.Fatalf("unexpected cookies: %#v", got)
	}
}

func TestLoadRejectsMalformedRecordWithoutEchoingSecret(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	err := Load(strings.NewReader(".example.test\tbad-secret\n"), jar)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "bad-secret") {
		t.Fatal("error exposed cookie value")
	}
}

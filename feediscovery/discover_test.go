package feediscovery_test

import (
	"bytes"
	"strings"
	"testing"
)

func TestDiscoverFeedURLHints(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, link string
		want       bool
	}{
		{"feed segment", `<link rel="alternate" href="/blog/feed/">`, true},
		{"RSS segment", `<link rel="alternate" href="/rss">`, true},
		{"Atom segment", `<link rel="alternate" href="/atom">`, true},
		{"nested RSS segment", `<link rel="alternate" href="/blog/RSS/">`, true},
		{"RSS suffix token", `<link rel="alternate" href="/blog-rss">`, true},
		{"feed suffix token", `<link rel="alternate" href="/blog-feed">`, true},
		{"extension", `<link rel="alternate" href="/index.XML">`, true},
		{"RSS title", `<link rel="alternate" href="/updates" title="Blog (RSS)">`, true},
		{"Atom title", `<link rel="alternate" href="/updates" title="ATOM feed">`, true},
		{"explicit relation", `<link rel="alternate FEED" href="/updates">`, true},
		{"explicit media type", `<link rel="alternate" href="/updates" type="application/atom+xml">`, true},
		{"feedback", `<link rel="alternate" href="/feedback">`, false},
		{"newsfeed", `<link rel="alternate" href="/newsfeed">`, false},
		{"anatomy path", `<link rel="alternate" href="/anatomy">`, false},
		{"RSSReader path", `<link rel="alternate" href="/RSSReader">`, false},
		{"anatomy", `<link rel="alternate" href="/updates" title="Anatomy">`, false},
		{"title suffix", `<link rel="alternate" href="/updates" title="RSSReader">`, false},
		{"hostname", `<link rel="alternate" href="https://feed.example.com">`, false},
		{"fragment", `<link rel="alternate" href="#feed">`, false},
		{"qualified same-document fragment", `<link rel="feed" href="#rss">`, false},
		{"absolute same-document fragment", `<link rel="feed" href="https://example.com/blog/#rss">`, false},
		{"unrelated relation", `<link rel="stylesheet" href="/feed.xml">`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := firstURL(t, []byte("<html><head>"+tt.link+"</head></html>"),
				"https://example.com/blog/", "text/html")
			if ok != tt.want {
				t.Errorf("discovered = %v, want %v", ok, tt.want)
			}
		})
	}
}

func TestDiscoveryCandidateFragmentUsesHTMLBase(t *testing.T) {
	t.Parallel()
	body := mustReadTestdata(t, "fragment-base.html")
	got, ok := firstURL(t, body, "https://example.com/blog/", "text/html")
	if !ok || got != "https://example.com/feed.xml" {
		t.Errorf("discovery = %q, %v", got, ok)
	}
}

func TestDiscoveryUsesBaseAfterLink(t *testing.T) {
	t.Parallel()
	body := mustReadTestdata(t, "late-base.html")
	for _, contentType := range []string{"text/html", "text/html; charset=iso-8859-1"} {
		got, ok := firstURL(t, body, "https://example.com/blog/", contentType)
		if !ok || got != "https://example.com/assets/feed.xml" {
			t.Errorf("discovery = %q, %v", got, ok)
		}
	}
}

func TestDiscoveryEmptyBaseTakesPrecedence(t *testing.T) {
	t.Parallel()
	for _, fixture := range []string{"empty-base.html", "whitespace-base.html", "bare-base.html"} {
		body := mustReadTestdata(t, fixture)
		got, ok := firstURL(t, body, "https://example.com/blog/index.html", "text/html")
		if !ok || got != "https://example.com/blog/feed.xml" {
			t.Errorf("%s: discovery = %q, %v", fixture, got, ok)
		}
	}
}

func TestDiscoveryXHTMLEncoding(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, fixture, contentType string
	}{
		{"default UTF-8", "utf8.xhtml", "application/xhtml+xml"},
		{"ignore HTML meta", "utf8-misleading-meta.xhtml", "application/xhtml+xml"},
		{"XML declaration", "latin1-declaration.xhtml", "application/xhtml+xml"},
		{"XML declaration whitespace", "latin1-declaration-tab.xhtml", "application/xhtml+xml"},
		{"HTTP charset", "latin1-http.xhtml", "application/xhtml+xml; charset=iso-8859-1"},
		{"HTTP overrides declaration", "utf8-http-override.xhtml", "application/xhtml+xml; charset=utf-8"},
		{"BOM overrides HTTP", "utf8-bom.xhtml", "application/xhtml+xml; charset=iso-8859-1"},
		{"UTF-16 big endian BOM", "utf16be-bom.xhtml", "application/xhtml+xml; charset=iso-8859-1"},
		{"UTF-16 little endian BOM", "utf16le-bom.xhtml", "application/xhtml+xml; charset=iso-8859-1"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body := mustReadTestdata(t, tt.fixture)
			if !strings.HasPrefix(tt.fixture, "utf16") && bytes.Index(body, []byte("href=")) <= 1024 {
				t.Fatal("fixture must place its non-ASCII URL beyond the charset sniff window")
			}
			got, ok := firstURL(t, body, "https://example.com/blog/", tt.contentType)
			if !ok || got != "https://example.com/caf%C3%A9.xml" {
				t.Errorf("discovery = %q, %v", got, ok)
			}
		})
	}
}

func TestDiscoveryMalformedFirstBase(t *testing.T) {
	t.Parallel()
	body := mustReadTestdata(t, "malformed-base.html")
	got, ok := firstURL(t, body, "https://example.com/blog/index.html", "text/html")
	if !ok || got != "https://example.com/blog/feed.xml" {
		t.Errorf("discovery = %q, %v", got, ok)
	}
}

func TestDiscoveryXHTMLUsesXMLTokens(t *testing.T) {
	t.Parallel()
	for _, fixture := range []string{
		"empty-template.xhtml", "nested-template.xhtml", "empty-script.xhtml",
		"template-links.xhtml", "foreign-namespace.xhtml",
	} {
		body := mustReadTestdata(t, fixture)
		got, ok := firstURL(t, body, "https://example.com/blog/", "application/xhtml+xml")
		if !ok || got != "https://example.com/correct/rss" {
			t.Errorf("%s: discovery = %q, %v", fixture, got, ok)
		}
	}
	body := mustReadTestdata(t, "prefixed-namespace.xhtml")
	if got, ok := firstURL(t, body, "https://example.com/", "application/xhtml+xml"); !ok || got != "https://example.com/rss" {
		t.Errorf("namespaced discovery = %q, %v", got, ok)
	}
	body = mustReadTestdata(t, "self-closing-template.html")
	if got, ok := firstURL(t, body, "https://example.com/", "text/html"); !ok || got != "https://example.com/rss" {
		t.Errorf("HTML template recovery = %q, %v", got, ok)
	}
}

func TestDiscoveryRecognizesLeadingHTMLComments(t *testing.T) {
	t.Parallel()
	document := mustReadTestdata(t, "feed-link.html")
	for _, prefix := range []string{"<!-- generated -->", "\xef\xbb\xbf\n<!-- generated -->\n"} {
		for _, contentType := range []string{"", "text/plain", "application/octet-stream"} {
			body := append([]byte(prefix), document...)
			got, ok := firstURL(t, body, "https://example.com/blog/", contentType)
			if !ok || got != "https://example.com/rss" {
				t.Errorf("discovery with %q prefix and %q content type = %q, %v", prefix, contentType, got, ok)
			}
		}
	}
}

func TestDiscoveryUsesFirstBaseRegardlessOfFetchability(t *testing.T) {
	t.Parallel()
	for _, fixture := range []string{"ftp-base", "userinfo-base"} {
		t.Run(fixture, func(t *testing.T) {
			body := mustReadTestdata(t, fixture+".html")
			if got, ok := firstURL(t, body, "https://example.com/blog/", "text/html"); ok {
				t.Errorf("non-fetchable base should not fall back to page/later base: %q", got)
			}
			body = mustReadTestdata(t, fixture+"-absolute-link.html")
			got, ok := firstURL(t, body, "https://example.com/blog/", "text/html")
			if !ok || got != "https://example.com/correct.xml" {
				t.Errorf("absolute fetchable candidate = %q, %v", got, ok)
			}
		})
	}
}

func TestDiscoveryHTMLIgnoresForeignNamespace(t *testing.T) {
	t.Parallel()
	for _, fixture := range []string{
		"foreign-namespace.html", "foreign-namespace-integration.html", "foreign-namespace-mathml.html",
	} {
		t.Run(fixture, func(t *testing.T) {
			body := mustReadTestdata(t, fixture)
			got, ok := firstURL(t, body, "https://example.com/blog/", "text/html")
			if !ok || got != "https://example.com/correct/rss" {
				t.Errorf("discovery = %q, %v", got, ok)
			}
		})
	}
}

func TestDiscoveryIgnoresTemplateContents(t *testing.T) {
	t.Parallel()
	body := mustReadTestdata(t, "templates.html")
	got, ok := firstURL(t, body, "https://example.com/blog/", "text/html")
	if !ok || got != "https://example.com/correct/feed.xml" {
		t.Errorf("discovery = %q, %v", got, ok)
	}
}

func TestDiscoverySkipsEquivalentSourceURLs(t *testing.T) {
	t.Parallel()
	for _, source := range []string{"http://example.com/blog", "https://example.com/blog"} {
		port := "80"
		if strings.HasPrefix(source, "https:") {
			port = "443"
		}
		equivalent := strings.Replace(source, "example.com", "EXAMPLE.COM:"+port, 1)
		body := []byte(`<html><link rel="feed" href="` + equivalent + `#rss"><link rel="feed" href="/actual"></html>`)
		got, ok := firstURL(t, body, source, "text/html")
		want := strings.TrimSuffix(source, "/blog") + "/actual"
		if !ok || got != want {
			t.Errorf("discovery = %q, %v, want %q", got, ok, want)
		}
	}

}

func TestDiscoverySniffsHTMLWithOmittedTags(t *testing.T) {
	t.Parallel()
	for _, prefix := range []string{
		"", "<title>Blog</title>", `<meta charset="utf-8">`, "<!-- generated -->\n",
	} {
		body := []byte(prefix + `<link rel="feed" href="/rss">`)
		got, ok := firstURL(t, body, "https://example.com/blog/", "text/plain")
		if !ok || got != "https://example.com/rss" {
			t.Errorf("discovery for %q = %q, %v", prefix, got, ok)
		}
	}
}

func TestDiscoveryAcceptsUppercaseSchemes(t *testing.T) {
	t.Parallel()
	for _, scheme := range []string{"HTTP", "HTTPS", "hTtPs"} {
		href := scheme + "://example.com/feed.xml"
		got, ok := firstURL(t, []byte(`<html><link rel="feed" href="`+href+`"></html>`),
			"https://example.com/blog/", "text/html")
		if !ok || got != strings.ToLower(scheme)+"://example.com/feed.xml" {
			t.Errorf("discovery for %q = %q, %v", href, got, ok)
		}

	}
}

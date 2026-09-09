package feediscovery_test

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/Songmu/rssnip/feediscovery"
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
	body := []byte(`<html><head><base href="/feed.xml"><link rel="feed" href="#rss"></head></html>`)
	got, ok := firstURL(t, body, "https://example.com/blog/", "text/html")
	if !ok || got != "https://example.com/feed.xml" {
		t.Errorf("discovery = %q, %v", got, ok)
	}
}

func TestDiscoveryUsesBaseAfterLink(t *testing.T) {
	t.Parallel()
	for _, contentType := range []string{"text/html", "text/html; charset=iso-8859-1"} {
		body := []byte(`<html><head>
		  <link rel="feed" href="feed.xml">
		  <base href="/assets/">
		  <base href="/ignored/">
		</head></html>`)
		got, ok := firstURL(t, body, "https://example.com/blog/", contentType)
		if !ok || got != "https://example.com/assets/feed.xml" {
			t.Errorf("discovery = %q, %v", got, ok)
		}
	}

}

func TestDiscoveryEmptyBaseTakesPrecedence(t *testing.T) {
	t.Parallel()
	for _, first := range []string{`href=""`, `href="   "`, `href`} {
		body := []byte(`<html><base target="_blank"><base ` + first + `>
		  <base href="/assets/"><link rel="feed" href="feed.xml"></html>`)
		got, ok := firstURL(t, body, "https://example.com/blog/index.html", "text/html")
		if !ok || got != "https://example.com/blog/feed.xml" {
			t.Errorf("base %s: discovery = %q, %v", first, got, ok)
		}
	}
}

func TestDiscoveryXHTMLEncoding(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, prefix, meta, path, contentType string
	}{
		{"default UTF-8", "", "", "/caf\xc3\xa9.xml", "application/xhtml+xml"},
		{"ignore HTML meta", "", `<meta charset="windows-1252" />`, "/caf\xc3\xa9.xml", "application/xhtml+xml"},
		{"XML declaration", `<?xml version="1.0" encoding="iso-8859-1"?>`, `<meta charset="utf-8" />`, "/caf\xe9.xml", "application/xhtml+xml"},
		{"XML declaration whitespace", "<?xml\tversion=\"1.0\" encoding=\"iso-8859-1\"?>", `<meta charset="utf-8" />`, "/caf\xe9.xml", "application/xhtml+xml"},
		{"HTTP charset", "", "", "/caf\xe9.xml", "application/xhtml+xml; charset=iso-8859-1"},
		{"HTTP overrides declaration", `<?xml version="1.0" encoding="iso-8859-1"?>`, "", "/caf\xc3\xa9.xml", "application/xhtml+xml; charset=utf-8"},
		{"BOM overrides HTTP", "\xef\xbb\xbf", "", "/caf\xc3\xa9.xml", "application/xhtml+xml; charset=iso-8859-1"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body := []byte(tt.prefix + `<html xmlns="http://www.w3.org/1999/xhtml"><head>` + tt.meta +
				"<!--" + strings.Repeat(" ", 2048) + "-->" +
				`<link rel="feed" href="` + tt.path + `" /></head></html>`)
			got, ok := firstURL(t, body, "https://example.com/blog/", tt.contentType)
			if !ok || got != "https://example.com/caf%C3%A9.xml" {
				t.Errorf("discovery = %q, %v", got, ok)
			}
		})
	}
	for _, littleEndian := range []bool{false, true} {
		body := []byte{0xfe, 0xff}
		if littleEndian {
			body = []byte{0xff, 0xfe}
		}
		for _, value := range utf16.Encode([]rune(`<html xmlns="http://www.w3.org/1999/xhtml"><head><link rel="feed" href="/caf` + "\u00e9" + `.xml" /></head></html>`)) {
			pair := []byte{byte(value >> 8), byte(value)}
			if littleEndian {
				pair[0], pair[1] = pair[1], pair[0]
			}
			body = append(body, pair...)
		}
		got, ok := firstURL(t, body, "https://example.com/blog/", "application/xhtml+xml; charset=iso-8859-1")
		if !ok || got != "https://example.com/caf%C3%A9.xml" {
			t.Errorf("UTF-16 little-endian=%v: discovery = %q, %v", littleEndian, got, ok)
		}
	}
}

func TestDiscoveryMalformedFirstBase(t *testing.T) {
	t.Parallel()
	body := []byte(`<html><base href="%zz"><base href="https://other.example/">
	  <link rel="feed" href="feed.xml"></html>`)
	got, ok := firstURL(t, body, "https://example.com/blog/index.html", "text/html")
	if !ok || got != "https://example.com/blog/feed.xml" {
		t.Errorf("discovery = %q, %v", got, ok)
	}
}

func TestDiscoveryXHTMLUsesXMLTokens(t *testing.T) {
	t.Parallel()
	for _, prefix := range []string{
		`<template/>`,
		`<template><template/></template>`,
		`<script/>`,
		`<template><base href="/wrong/"/><link rel="feed" href="/wrong"/></template>`,
		`<svg xmlns="http://www.w3.org/2000/svg"><link rel="feed" href="/wrong"/></svg>`,
	} {
		body := []byte(`<html xmlns="http://www.w3.org/1999/xhtml"><head>` + prefix +
			`<link rel="feed" href="rss"/><base href="/correct/"/></head></html>`)
		got, ok := firstURL(t, body, "https://example.com/blog/", "application/xhtml+xml")
		if !ok || got != "https://example.com/correct/rss" {
			t.Errorf("prefix %q: discovery = %q, %v", prefix, got, ok)
		}
	}
	body := []byte(`<h:html xmlns:h="http://www.w3.org/1999/xhtml"><h:head>
	  <h:template/><h:link rel="feed" href="/rss"/></h:head></h:html>`)
	if got, ok := firstURL(t, body, "https://example.com/", "application/xhtml+xml"); !ok || got != "https://example.com/rss" {
		t.Errorf("namespaced discovery = %q, %v", got, ok)
	}
	body = []byte(`<html><head><template/><link rel="feed" href="/hidden"></template>
	  <link rel="feed" href="/rss"></head></html>`)
	if got, ok := firstURL(t, body, "https://example.com/", "text/html"); !ok || got != "https://example.com/rss" {
		t.Errorf("HTML template recovery = %q, %v", got, ok)
	}
}

func TestDiscoveryRecognizesLeadingHTMLComments(t *testing.T) {
	t.Parallel()
	for _, prefix := range []string{"<!-- generated -->", "\xef\xbb\xbf\n<!-- generated -->\n"} {
		for _, contentType := range []string{"", "text/plain", "application/octet-stream"} {
			body := []byte(prefix + `<html><link rel="feed" href="/rss"></html>`)
			got, ok := firstURL(t, body, "https://example.com/blog/", contentType)
			if !ok || got != "https://example.com/rss" {
				t.Errorf("discovery with %q prefix and %q content type = %q, %v", prefix, contentType, got, ok)
			}
		}
	}
}

func TestDiscoveryUsesFirstBaseRegardlessOfFetchability(t *testing.T) {
	t.Parallel()
	for _, base := range []string{
		"ftp://example.com/assets/",
		"https://user:password@example.com/assets/",
	} {
		t.Run(base, func(t *testing.T) {
			body := `<html><head><base href="` + base + `">
			  <base href="https://example.com/incorrect/">
			  <link rel="feed" href="relative.xml">
			</head></html>`
			if got, ok := firstURL(t, []byte(body), "https://example.com/blog/", "text/html"); ok {
				t.Errorf("non-fetchable base should not fall back to page/later base: %q", got)
			}
			body = strings.Replace(body, "</head>", `<link rel="feed" href="https://example.com/correct.xml"></head>`, 1)
			got, ok := firstURL(t, []byte(body), "https://example.com/blog/", "text/html")
			if !ok || got != "https://example.com/correct.xml" {
				t.Errorf("absolute fetchable candidate = %q, %v", got, ok)
			}
		})
	}
}

func TestDiscoveryIgnoresTemplateContents(t *testing.T) {
	t.Parallel()
	body := []byte(`<html><head>
	  <template><base href="/wrong/"><link rel="feed" href="/placeholder">
	    <template><base href="/also-wrong/"><link rel="feed" href="/nested"></template>
	  </template>
	  <link rel="feed" href="feed.xml"><base href="/correct/">
	</head></html>`)
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

func firstURL(t *testing.T, body []byte, pageURL, contentType string) (string, bool) {
	t.Helper()
	links, err := feediscovery.FindAll(bytes.NewReader(body), pageURL, contentType)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) == 0 {
		return "", false
	}
	return links[0].URL, true
}

package rssnip_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Songmu/rssnip"
)

const jsonFeedPage = `{"version":"https://jsonfeed.org/version/1.1","title":"Example",%s
  "items":[{"id":%q,"url":"https://example.com/%s","date_published":%q}]}`

func jsonFeedDocument(id, date, nextURL string) string {
	next := ""
	if nextURL != "" {
		next = fmt.Sprintf("\"next_url\":%q,", nextURL)
	}
	return fmt.Sprintf(jsonFeedPage, next, id, id, date)
}

// userinfoURL builds a URL carrying credentials that must never be echoed back.
func userinfoURL() string {
	return "https://user:" + "hunter2" + "@example.com/feed.json"
}

func itemIDs(items []rssnip.Item) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func TestFetchFollowsPaginationAndDiscovery(t *testing.T) {
	t.Parallel()
	var server *httptest.Server
	var requests []string
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path)
		switch r.URL.Path {
		case "/blog":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintf(w, `<html><head><link rel="alternate" type="application/feed+json" href="/feed.json"></head></html>`)
		case "/feed.json":
			w.Header().Set("Content-Type", "application/feed+json")
			fmt.Fprint(w, jsonFeedDocument("first", "2026-01-03T00:00:00Z", server.URL+"/feed2.json"))
		case "/feed2.json":
			w.Header().Set("Content-Type", "application/feed+json")
			fmt.Fprint(w, jsonFeedDocument("second", "2026-01-02T00:00:00Z", server.URL+"/feed3.json"))
		default:
			w.Header().Set("Content-Type", "application/feed+json")
			fmt.Fprint(w, jsonFeedDocument("third", "2026-01-01T00:00:00Z", ""))
		}
	}))
	t.Cleanup(server.Close)

	items, err := rssnip.Fetch(context.Background(), server.URL+"/blog",
		rssnip.WithHTTPClient(server.Client()), rssnip.WithMaxPages(2))
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got, want := strings.Join(itemIDs(items), ","), "first,second"; got != want {
		t.Errorf("item IDs = %q, want %q", got, want)
	}
	if got, want := strings.Join(requests, ","), "/blog,/feed.json,/feed2.json"; got != want {
		t.Errorf("requests = %q, want %q", got, want)
	}
	if items[0].Feed.FeedURL != server.URL+"/feed.json" {
		t.Errorf("feed URL = %q, want %q", items[0].Feed.FeedURL, server.URL+"/feed.json")
	}
}

func TestFetchWithoutDiscoveryRejectsHTMLPage(t *testing.T) {
	t.Parallel()
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><head><link rel="alternate" type="application/feed+json" href="/feed.json"></head></html>`)
	}))
	t.Cleanup(server.Close)

	_, err := rssnip.Fetch(context.Background(), server.URL+"/blog",
		rssnip.WithHTTPClient(server.Client()), rssnip.WithDiscovery(false))
	if !errors.Is(err, rssnip.ErrNotFeed) {
		t.Fatalf("error = %v, want ErrNotFeed", err)
	}
	if errors.Is(err, rssnip.ErrNoFeedFound) {
		t.Errorf("error = %v, want no ErrNoFeedFound for a skipped discovery", err)
	}
	if requests != 1 {
		t.Errorf("requests = %d, want 1", requests)
	}
}

func TestFetchReportsMissingFeedLink(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><head><title>No feed</title></head></html>`)
	}))
	t.Cleanup(server.Close)

	_, err := rssnip.Fetch(context.Background(), server.URL+"/blog",
		rssnip.WithHTTPClient(server.Client()))
	if !errors.Is(err, rssnip.ErrNoFeedFound) || !errors.Is(err, rssnip.ErrNotFeed) {
		t.Fatalf("error = %v, want ErrNoFeedFound and ErrNotFeed", err)
	}
}

func TestFetchReportsHTTPStatus(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "gone", http.StatusGone)
	}))
	t.Cleanup(server.Close)

	_, err := rssnip.Fetch(context.Background(), server.URL+"/feed.json",
		rssnip.WithHTTPClient(server.Client()))
	var statusErr *rssnip.StatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("error = %v, want *StatusError", err)
	}
	if statusErr.StatusCode != http.StatusGone {
		t.Errorf("status code = %d, want %d", statusErr.StatusCode, http.StatusGone)
	}
	if statusErr.URL != server.URL+"/feed.json" {
		t.Errorf("URL = %q, want %q", statusErr.URL, server.URL+"/feed.json")
	}
	if !strings.Contains(statusErr.Error(), statusErr.Status) {
		t.Errorf("error = %q, want the HTTP status", statusErr.Error())
	}
}

func TestFetchSendsConfiguredUserAgent(t *testing.T) {
	t.Parallel()
	var agents []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agents = append(agents, r.Header.Get("User-Agent"))
		w.Header().Set("Content-Type", "application/feed+json")
		fmt.Fprint(w, jsonFeedDocument("first", "2026-01-01T00:00:00Z", ""))
	}))
	t.Cleanup(server.Close)

	if _, err := rssnip.Fetch(context.Background(), server.URL+"/feed.json",
		rssnip.WithHTTPClient(server.Client()), rssnip.WithUserAgent("example/1.0")); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if want := []string{"example/1.0"}; len(agents) != 1 || agents[0] != want[0] {
		t.Errorf("user agents = %v, want %v", agents, want)
	}
}

func TestFetchFiltersByPeriod(t *testing.T) {
	t.Parallel()
	document := `{"version":"https://jsonfeed.org/version/1.1","items":[
	  {"id":"new","date_published":"2026-01-05T00:00:00Z"},
	  {"id":"boundary","date_published":"2026-01-03T00:00:00Z"},
	  {"id":"old","date_published":"2026-01-01T00:00:00Z"},
	  {"id":"undated"}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/feed+json")
		fmt.Fprint(w, document)
	}))
	t.Cleanup(server.Close)

	items, err := rssnip.Fetch(context.Background(), server.URL+"/feed.json",
		rssnip.WithHTTPClient(server.Client()),
		rssnip.WithSince(time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)),
		rssnip.WithUntil(time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got, want := strings.Join(itemIDs(items), ","), "boundary"; got != want {
		t.Errorf("item IDs = %q, want %q", got, want)
	}
}

func TestFetchPrefersUpdatedDate(t *testing.T) {
	t.Parallel()
	document := `{"version":"https://jsonfeed.org/version/1.1","items":[
	  {"id":"revised","date_published":"2026-01-01T00:00:00Z","date_modified":"2026-01-05T00:00:00Z"}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/feed+json")
		fmt.Fprint(w, document)
	}))
	t.Cleanup(server.Close)

	since := rssnip.WithSince(time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC))
	items, err := rssnip.Fetch(context.Background(), server.URL+"/feed.json",
		rssnip.WithHTTPClient(server.Client()), since, rssnip.WithPreferUpdated(true))
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got, want := strings.Join(itemIDs(items), ","), "revised"; got != want {
		t.Errorf("item IDs = %q, want %q", got, want)
	}
	items, err = rssnip.Fetch(context.Background(), server.URL+"/feed.json",
		rssnip.WithHTTPClient(server.Client()), since)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("items = %v, want none when filtering by the published date", itemIDs(items))
	}
}

func TestFetchRejectsInvalidOptionsAndURLs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		url     string
		options []rssnip.Option
		want    string
	}{
		{"nil option", "https://example.com/feed.json", []rssnip.Option{nil}, "option must not be nil"},
		{"nil client", "https://example.com/feed.json",
			[]rssnip.Option{rssnip.WithHTTPClient(nil)}, "HTTP client must not be nil"},
		{"empty user agent", "https://example.com/feed.json",
			[]rssnip.Option{rssnip.WithUserAgent("  ")}, "user agent must not be empty"},
		{"zero pages", "https://example.com/feed.json",
			[]rssnip.Option{rssnip.WithMaxPages(0)}, "max pages must be at least 1"},
		{"reversed period", "https://example.com/feed.json", []rssnip.Option{
			rssnip.WithSince(time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)),
			rssnip.WithUntil(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
		}, "since must not be after until"},
		{"relative URL", "/feed.json", nil, "invalid feed URL"},
		{"unsupported scheme", "file:///feed.json", nil, "invalid feed URL"},
		{"userinfo", userinfoURL(), nil, "userinfo is not allowed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := rssnip.Fetch(context.Background(), tt.url, tt.options...)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
			if strings.Contains(err.Error(), "hunter2") {
				t.Errorf("error = %v, exposes userinfo", err)
			}
		})
	}
}

func TestFetchRejectsNilContext(t *testing.T) {
	t.Parallel()
	//nolint:staticcheck // the nil context is the behavior under test
	_, err := rssnip.Fetch(nil, "https://example.com/feed.json")
	if err == nil || !strings.Contains(err.Error(), "context must not be nil") {
		t.Fatalf("error = %v, want a nil context error", err)
	}
}

func TestParseReadsFeedWithoutNetworkAccess(t *testing.T) {
	t.Parallel()
	document := `<?xml version="1.0" encoding="UTF-8"?>
	<rss version="2.0"><channel><title>Example</title>
	  <item><title>Hello</title><link>/posts/1</link>
	    <pubDate>Fri, 02 Jan 2026 03:04:05 GMT</pubDate></item>
	</channel></rss>`
	items, err := rssnip.Parse(strings.NewReader(document),
		"https://example.com/feed.xml", "application/rss+xml")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %v, want one item", itemIDs(items))
	}
	if items[0].URL != "https://example.com/posts/1" {
		t.Errorf("URL = %q, want the resolved item URL", items[0].URL)
	}
	if items[0].Feed.FeedURL != "https://example.com/feed.xml" {
		t.Errorf("feed URL = %q, want the source URL", items[0].Feed.FeedURL)
	}
	if items[0].DatePublished != "2026-01-02T03:04:05Z" {
		t.Errorf("date = %q, want RFC3339", items[0].DatePublished)
	}
}

func TestParseIgnoresPaginationLinks(t *testing.T) {
	t.Parallel()
	document := jsonFeedDocument("first", "2026-01-03T00:00:00Z", "https://example.com/feed2.json")
	items, err := rssnip.Parse(strings.NewReader(document),
		"https://example.com/feed.json", "application/feed+json", rssnip.WithMaxPages(5))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got, want := strings.Join(itemIDs(items), ","), "first"; got != want {
		t.Errorf("item IDs = %q, want %q", got, want)
	}
}

func TestParseFiltersByPeriod(t *testing.T) {
	t.Parallel()
	document := `{"version":"https://jsonfeed.org/version/1.1","items":[
	  {"id":"new","date_published":"2026-01-05T00:00:00Z"},
	  {"id":"old","date_published":"2026-01-01T00:00:00Z"}]}`
	items, err := rssnip.Parse(strings.NewReader(document),
		"https://example.com/feed.json", "application/feed+json",
		rssnip.WithSince(time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got, want := strings.Join(itemIDs(items), ","), "new"; got != want {
		t.Errorf("item IDs = %q, want %q", got, want)
	}
}

func TestParseRejectsHTMLDocument(t *testing.T) {
	t.Parallel()
	documents := map[string]string{
		"doctype": `<!DOCTYPE html><html><head>
	  <link rel="alternate" type="application/rss+xml" href="/feed.xml"></head></html>`,
		"byte order mark and comment": "\ufeff<!-- hello --><!DOCTYPE html><html></html>",
		"head only":                   `<head><title>Blog</title></head>`,
	}
	for name, document := range documents {
		for _, contentType := range []string{"text/html; charset=utf-8", ""} {
			_, err := rssnip.Parse(strings.NewReader(document), "https://example.com/blog", contentType)
			if !errors.Is(err, rssnip.ErrNotFeed) {
				t.Fatalf("%s with content type %q: error = %v, want ErrNotFeed",
					name, contentType, err)
			}
			if !strings.Contains(err.Error(), "feediscovery.FindAll") {
				t.Errorf("%s with content type %q: error = %v, want a discovery hint",
					name, contentType, err)
			}
		}
	}
}

func TestParseRejectsInvalidInput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		reader    io.Reader
		sourceURL string
		want      string
	}{
		{"nil reader", nil, "https://example.com/feed.json", "document reader is nil"},
		{"relative source URL", strings.NewReader("{}"), "/feed.json",
			"must be an absolute HTTP or HTTPS URL"},
		{"source URL with userinfo", strings.NewReader("{}"),
			userinfoURL(), "must not contain userinfo"},
		{"unsupported document", strings.NewReader("not a feed"),
			"https://example.com/feed.json", "parse feed"},
		{"oversized document", strings.NewReader(strings.Repeat("a", rssnip.MaxFeedSize+1)),
			"https://example.com/feed.json", "feed exceeds 32 MiB limit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := rssnip.Parse(tt.reader, tt.sourceURL, "")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
			if strings.Contains(err.Error(), "hunter2") {
				t.Errorf("error = %v, exposes userinfo", err)
			}
		})
	}
}

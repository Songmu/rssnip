package rssnip_test

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/Songmu/rssnip"
)

func ExampleParse() {
	document := `{
	  "version": "https://jsonfeed.org/version/1.1",
	  "title": "Example",
	  "items": [
	    {"id": "1", "url": "/posts/1", "title": "Hello", "date_published": "2026-01-02T03:04:05Z"}
	  ]
	}`
	items, err := rssnip.Parse(
		strings.NewReader(document),
		"https://example.com/feed.json",
		"application/feed+json",
	)
	if err != nil {
		log.Fatal(err)
	}
	for _, item := range items {
		fmt.Printf("%s %s %s\n", item.Feed.Title, item.Title, item.DatePublished)
	}
	// Output:
	// Example Hello 2026-01-02T03:04:05Z
}

func ExampleFetch() {
	document := `<?xml version="1.0" encoding="UTF-8"?>
	<rss version="2.0"><channel>
	  <title>Example</title>
	  <item>
	    <title>Hello</title>
	    <link>https://example.com/posts/1</link>
	    <pubDate>Fri, 02 Jan 2026 03:04:05 GMT</pubDate>
	  </item>
	</channel></rss>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		fmt.Fprint(w, document)
	}))
	defer server.Close()

	items, err := rssnip.Fetch(
		context.Background(),
		server.URL+"/feed.xml",
		rssnip.WithHTTPClient(server.Client()),
		rssnip.WithMaxPages(1),
	)
	if err != nil {
		log.Fatal(err)
	}
	for _, item := range items {
		fmt.Printf("%s %s %s\n", item.Title, item.URL, item.DatePublished)
	}
	// Output:
	// Hello https://example.com/posts/1 2026-01-02T03:04:05Z
}

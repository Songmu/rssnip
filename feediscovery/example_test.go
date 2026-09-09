package feediscovery_test

import (
	"fmt"
	"log"
	"strings"

	"github.com/Songmu/rssnip/feediscovery"
)

func ExampleFindAll() {
	document := `<html><head>
	  <link rel="alternate" href="rss.xml" title="News" type="application/rss+xml">
	  <link rel="feed" href="atom.xml" title="News (Atom)" type="application/atom+xml">
	</head></html>`
	links, err := feediscovery.FindAll(
		strings.NewReader(document),
		"https://example.com/blog/",
		"text/html; charset=utf-8",
	)
	if err != nil {
		log.Fatal(err)
	}
	for _, link := range links {
		fmt.Printf("%s %q %s\n", link.URL, link.Title, link.Type)
	}
	// Output:
	// https://example.com/blog/rss.xml "News" application/rss+xml
	// https://example.com/blog/atom.xml "News (Atom)" application/atom+xml
}

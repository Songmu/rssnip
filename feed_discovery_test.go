package rssnip

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestRunDiscoverySkipsNonFeedAlternates(t *testing.T) {
	t.Parallel()
	feed := mustReadTestdata(t, "sample_rss.xml")
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path)
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html><head>
			  <link rel="alternate" href="/feedback">
			  <link rel="alternate" href="/anatomy" title="Anatomy">
			  <link rel="alternate" href="/newsfeed">
			  <link rel="alternate" href="/feed.xml" type="application/rss+xml">
			  <link rel="feed" href="/second.xml">
			</head></html>`)
		case "/feed.xml":
			w.Header().Set("Content-Type", "application/rss+xml")
			w.Write(feed)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"--jq", ".id", server.URL},
		strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if stdout.Len() == 0 {
		t.Fatal("no feed items emitted")
	}
	// Close waits for handlers before inspecting their request log.
	server.Close()
	if want := []string{"/", "/feed.xml"}; !reflect.DeepEqual(requests, want) {
		t.Errorf("requests = %v, want %v", requests, want)
	}
}

func TestDiscoverFeedURLHints(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, link string
		want       bool
	}{
		{"feed segment", `<link rel="alternate" href="/blog/feed/">`, true},
		{"feed suffix token", `<link rel="alternate" href="/blog-feed">`, true},
		{"extension", `<link rel="alternate" href="/index.XML">`, true},
		{"RSS title", `<link rel="alternate" href="/updates" title="Blog (RSS)">`, true},
		{"Atom title", `<link rel="alternate" href="/updates" title="ATOM feed">`, true},
		{"explicit relation", `<link rel="alternate FEED" href="/updates">`, true},
		{"explicit media type", `<link rel="alternate" href="/updates" type="application/atom+xml">`, true},
		{"feedback", `<link rel="alternate" href="/feedback">`, false},
		{"newsfeed", `<link rel="alternate" href="/newsfeed">`, false},
		{"anatomy", `<link rel="alternate" href="/updates" title="Anatomy">`, false},
		{"title suffix", `<link rel="alternate" href="/updates" title="RSSReader">`, false},
		{"hostname", `<link rel="alternate" href="https://feed.example.com">`, false},
		{"fragment", `<link rel="alternate" href="#feed">`, false},
		{"unrelated relation", `<link rel="stylesheet" href="/feed.xml">`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := discoverFeedURL([]byte("<html><head>"+tt.link+"</head></html>"),
				"https://example.com/blog/", "text/html")
			if ok != tt.want {
				t.Errorf("discovered = %v, want %v", ok, tt.want)
			}
		})
	}
}

func TestHelpDescribesBlogURLInputs(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"-h"}, strings.NewReader(""), &stdout, &stderr)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("error = %v, want flag.ErrHelp", err)
	}
	for _, want := range []string{
		"Feed or blog/site URLs", "positional arguments", "standard input",
		"feed or blog/site URL (repeatable)",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("help missing %q:\n%s", want, stderr.String())
		}
	}
}

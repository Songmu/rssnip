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

	"github.com/Songmu/rssnip/feediscovery"
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
			  <link rel="feed" href="#rss">
			  <link rel="alternate" type="application/rss+xml" href="/#atom">
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

func TestRunDiscoveryReturnsFirstCandidateFailure(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"fetch", "parse"} {
		t.Run(failure, func(t *testing.T) {
			var requests []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.URL.Path)
				switch r.URL.Path {
				case "/":
					w.Header().Set("Content-Type", "text/html")
					fmt.Fprint(w, `<html><head>
					  <link rel="feed" href="/first">
					  <link rel="feed" href="/second">
					</head></html>`)
				case "/first":
					if failure == "fetch" {
						http.NotFound(w, r)
					} else {
						fmt.Fprint(w, "not a feed")
					}
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			var stdout, stderr bytes.Buffer
			err := run(context.Background(), []string{server.URL}, strings.NewReader(""), &stdout, &stderr)
			wantError := "404 Not Found"
			if failure == "parse" {
				wantError = "parse feed"
			}
			if err == nil || !strings.Contains(err.Error(), wantError) || !strings.Contains(err.Error(), "/first") {
				t.Fatalf("error = %v, want %q for first candidate", err, wantError)
			}
			if stdout.Len() != 0 {
				t.Errorf("unexpected output: %s", stdout.String())
			}
			server.Close()
			if want := []string{"/", "/first"}; !reflect.DeepEqual(requests, want) {
				t.Errorf("requests = %v, want %v", requests, want)
			}
		})
	}
}

func TestRunDiscoveryReportsDocumentErrors(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, contentType, body, wantError string }{
		{
			"malformed tail", "application/xhtml+xml",
			`<html xmlns="http://www.w3.org/1999/xhtml"><base href="/"/>
			  <link rel="feed" href="/first"/><link rel="feed" href="/second"/><bad></wrong></html>`,
			"scan feed links",
		},
		{
			"unknown encoding", "application/xhtml+xml; charset=unknown-charset",
			`<html xmlns="http://www.w3.org/1999/xhtml"><link rel="feed" href="/first"/></html>`,
			"decode document",
		},
		{
			"excessive nesting", "text/html",
			`<html><base href="/"/><link rel="feed" href="/first"/><svg>` +
				strings.Repeat("<g>", feediscovery.MaxNestingDepth) +
				strings.Repeat("</g>", feediscovery.MaxNestingDepth) + "</svg></html>",
			"document nesting exceeds limit",
		},
		{"no candidates", "text/html", `<html><title>No feed</title></html>`, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var requests []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.URL.Path)
				if r.URL.Path == "/blog" {
					w.Header().Set("Content-Type", tt.contentType)
					fmt.Fprint(w, tt.body)
					return
				}
				http.NotFound(w, r)
			}))
			defer server.Close()
			var stdout, stderr bytes.Buffer
			err := run(context.Background(), []string{server.URL + "/blog"}, strings.NewReader(""), &stdout, &stderr)
			if err == nil {
				t.Fatal("expected a discovery or feed parsing error")
			}
			_, parseErr := parseFeed([]byte(tt.body), server.URL+"/blog")
			if parseErr == nil || !strings.Contains(err.Error(), parseErr.Error()) {
				t.Fatalf("error = %v, want original parse error %v", err, parseErr)
			}
			if tt.wantError == "" {
				if err.Error() != parseErr.Error() {
					t.Errorf("no candidates: error = %v, want unchanged parse error", err)
				}
			} else if !strings.Contains(err.Error(), "discover feed links:") || !strings.Contains(err.Error(), tt.wantError) {
				t.Errorf("error = %v, want discovery %q error", err, tt.wantError)
			}
			if stdout.Len() != 0 {
				t.Errorf("unexpected output: %s", stdout.String())
			}
			server.Close()
			if want := []string{"/blog"}; !reflect.DeepEqual(requests, want) {
				t.Errorf("requests = %v, want %v", requests, want)
			}
		})
	}
}

func TestRunDiscoveryUsesRedirectedPageAndFeedPagination(t *testing.T) {
	t.Parallel()
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path)
		switch r.URL.Path {
		case "/start":
			http.Redirect(w, r, "/blog/index.html", http.StatusFound)
		case "/blog/index.html":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html><link rel="feed" href="feed.json"><link rel="feed" href="other.json"></html>`)
		case "/blog/feed.json":
			w.Header().Set("Content-Type", "application/feed+json")
			fmt.Fprint(w, `{"version":"https://jsonfeed.org/version/1.1","items":[{"id":"one"}],"next_url":"next.json"}`)
		case "/blog/next.json":
			w.Header().Set("Content-Type", "application/feed+json")
			fmt.Fprint(w, `{"version":"https://jsonfeed.org/version/1.1","items":[{"id":"two"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"--jq", ".id", "-r", server.URL + "/start"},
		strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "one\ntwo\n" || stderr.Len() != 0 {
		t.Errorf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
	}
	server.Close()
	want := []string{"/start", "/blog/index.html", "/blog/feed.json", "/blog/next.json"}
	if !reflect.DeepEqual(requests, want) {
		t.Errorf("requests = %v, want %v", requests, want)
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
		"Usage: rssnip [options] [URL ...]",
		"Feed or blog/site URLs", "positional arguments", "standard input",
		"Positional URLs are processed first.", "Place options before URLs.",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("help missing %q:\n%s", want, stderr.String())
		}
	}
	if strings.Contains(stderr.String(), "-url") {
		t.Errorf("help contains removed URL option:\n%s", stderr.String())
	}
}

func TestDiscoveryDoesNotFollowHTMLPagination(t *testing.T) {
	t.Parallel()
	for _, initial := range []string{"/blog", "/first"} {
		t.Run(initial, func(t *testing.T) {
			var requests []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.URL.Path)
				switch r.URL.Path {
				case "/blog":
					w.Header().Set("Content-Type", "text/html")
					fmt.Fprint(w, `<html><head><link rel="feed" href="/first"></head></html>`)
				case "/first":
					w.Header().Set("Content-Type", "application/feed+json")
					fmt.Fprint(w, `{"version":"https://jsonfeed.org/version/1.1","next_url":"/archive","items":[{"id":"one"}]}`)
				case "/archive":
					w.Header().Set("Content-Type", "text/html")
					fmt.Fprint(w, `<html><head><link rel="feed" href="/different"></head></html>`)
				case "/different":
					w.Header().Set("Content-Type", "application/feed+json")
					fmt.Fprint(w, `{"version":"https://jsonfeed.org/version/1.1","items":[{"id":"unrelated"}]}`)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			var stdout, stderr bytes.Buffer
			err := run(context.Background(), []string{server.URL + initial}, strings.NewReader(""), &stdout, &stderr)
			if err == nil || !strings.Contains(err.Error(), "parse feed") || !strings.Contains(err.Error(), "/archive") {
				t.Fatalf("error = %v, want archive parse error", err)
			}
			server.Close()
			want := []string{"/first", "/archive"}
			if initial == "/blog" {
				want = append([]string{"/blog"}, want...)
			}
			if !reflect.DeepEqual(requests, want) {
				t.Errorf("requests = %v, want %v", requests, want)
			}
			if stdout.Len() != 0 {
				t.Errorf("unexpected output after pagination failure: %s", stdout.String())
			}
		})
	}
}

func TestRunDiscoveryAcceptsUppercaseSchemes(t *testing.T) {
	t.Parallel()

	feed := mustReadTestdata(t, "sample_rss.xml")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/blog":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintf(w, `<html><link rel="feed" href="HTTP://%s/feed.xml"></html>`, r.Host)
		case "/feed.xml":
			w.Header().Set("Content-Type", "application/rss+xml")
			w.Write(feed)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	for _, input := range []string{
		server.URL + "/blog",
		strings.Replace(server.URL, "http:", "HTTP:", 1) + "/feed.xml",
	} {
		var stdout, stderr bytes.Buffer
		if err := run(context.Background(), []string{input}, strings.NewReader(""), &stdout, &stderr); err != nil {
			t.Fatalf("run %q: %v", input, err)
		}
		if stdout.Len() == 0 {
			t.Errorf("no items for %q", input)
		}
	}
}

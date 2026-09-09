// This file covers network-level behavior of feed fetching: error
// propagation, cancellation, and redirect handling.
package rssnip

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type errorTransport struct{ err error }

func (transport errorTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, transport.err
}

func TestFetchFeedPreservesNetworkErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		cause error
	}{
		{"canceled", context.Canceled},
		{"deadline exceeded", context.DeadlineExceeded},
		{"dns error", &net.DNSError{Err: "no such host", Name: "example.invalid", IsNotFound: true}},
		{"generic error", errors.New("TLS handshake failed")},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			client := &http.Client{Transport: errorTransport{err: tt.cause}}
			_, err := fetchFeed(context.Background(), client, "https://example.invalid/feed")
			if !errors.Is(err, tt.cause) {
				t.Errorf("error = %v, want wrapped %v", err, tt.cause)
			}
			if err == nil || !strings.Contains(err.Error(), tt.cause.Error()) {
				t.Errorf("error = %v, missing cause %v", err, tt.cause)
			}
		})
	}
}

func TestRunPreservesCanceledFetch(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	server := newFeedServer(t, string(mustReadTestdata(t, "sample_rss.xml")))
	var stdout, stderr strings.Builder
	err := Run(ctx, []string{server.URL}, &stdout, &stderr)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want wrapped context.Canceled", err)
	}
	if stdout.Len() != 0 {
		t.Errorf("unexpected output: %q", stdout.String())
	}
}

func TestFetchFeedRedactsRedirectErrors(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://user:"+"pass"+"word@example.com/feed", http.StatusFound)
	}))
	t.Cleanup(server.Close)
	cause := errors.New("redirect denied")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return cause
	}}
	_, err := fetchFeed(context.Background(), client, server.URL)
	if !errors.Is(err, cause) {
		t.Fatalf("error = %v, want wrapped %v", err, cause)
	}
	if strings.Contains(err.Error(), "password") || strings.Contains(err.Error(), "user:") {
		t.Errorf("error exposes redirect credentials: %v", err)
	}
}

func TestFetchFeedPagesFollowsAtomAndJSONFeedPagination(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		pages map[string]string
	}{
		{
			name: "Atom link relation",
			pages: map[string]string{
				"/atom/one": `<feed xmlns="http://www.w3.org/2005/Atom"><link rel="next" href="two"/><entry><id>one</id><title>One</title></entry></feed>`,
				"/atom/two": `<feed xmlns="http://www.w3.org/2005/Atom"><entry><id>two</id><title>Two</title></entry></feed>`,
			},
		},
		{
			name: "RSS Atom link relation",
			pages: map[string]string{
				"/rss/one": `<rss xmlns:atom="http://www.w3.org/2005/Atom"><channel><title>Feed</title><atom:link rel="next" href="two"/><item><guid>one</guid><title>One</title></item></channel></rss>`,
				"/rss/two": `<rss><channel><title>Feed</title><item><guid>two</guid><title>Two</title></item></channel></rss>`,
			},
		},
		{
			name: "JSON Feed next URL",
			pages: map[string]string{
				"/json/one": `{"version":"https://jsonfeed.org/version/1.1","next_url":"two","items":[{"id":"one","title":"One"}]}`,
				"/json/two": `{"version":"https://jsonfeed.org/version/1.1","items":[{"id":"two","title":"Two"}]}`,
			},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, ok := tt.pages[r.URL.Path]
				if !ok {
					http.NotFound(w, r)
					return
				}
				w.Write([]byte(body))
			}))
			t.Cleanup(server.Close)
			var firstPath string
			for path := range tt.pages {
				if strings.HasSuffix(path, "/one") {
					firstPath = path
				}
			}
			items, err := fetchFeedPages(context.Background(), server.Client(), server.URL+firstPath, 2)
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != 2 || items[0].ID != "one" || items[1].ID != "two" {
				t.Fatalf("items = %#v", items)
			}
			if items[1].Feed.FeedURL != server.URL+strings.TrimSuffix(firstPath, "one")+"two" {
				t.Errorf("second feed URL = %q", items[1].Feed.FeedURL)
			}
		})
	}
}

func TestFetchFeedPagesBoundsAndDeduplicates(t *testing.T) {
	t.Parallel()
	var requests, pageThreeRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		switch r.URL.Path {
		case "/one":
			w.Write([]byte(`{"version":"https://jsonfeed.org/version/1.1","next_url":"/two","items":[{"id":"one"},{"id":"duplicate"}]}`))
		case "/two":
			w.Write([]byte(`{"version":"https://jsonfeed.org/version/1.1","next_url":"/three","items":[{"id":"duplicate"},{"id":"two"}]}`))
		case "/three":
			pageThreeRequests++
			w.Write([]byte(`{"version":"https://jsonfeed.org/version/1.1","items":[{"id":"three"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	items, err := fetchFeedPages(context.Background(), server.Client(), server.URL+"/one", 2)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Errorf("requests = %d, want 2", requests)
	}
	if pageThreeRequests != 0 {
		t.Errorf("page three requests = %d, want 0", pageThreeRequests)
	}
	if len(items) != 3 || items[0].ID != "one" || items[1].ID != "duplicate" || items[2].ID != "two" {
		t.Errorf("items = %#v", items)
	}
}

func TestFetchFeedPagesStopsCycles(t *testing.T) {
	t.Parallel()
	var oneRequests, twoRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/one":
			oneRequests++
			w.Write([]byte(`{"version":"https://jsonfeed.org/version/1.1","next_url":"/two","items":[{"id":"one"}]}`))
		case "/two":
			twoRequests++
			w.Write([]byte(`{"version":"https://jsonfeed.org/version/1.1","next_url":"/one","items":[{"id":"two"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	items, err := fetchFeedPages(context.Background(), server.Client(), server.URL+"/one", 10)
	if err != nil {
		t.Fatal(err)
	}
	if oneRequests != 1 || twoRequests != 1 {
		t.Errorf("requests = /one: %d, /two: %d, want 1 each", oneRequests, twoRequests)
	}
	if len(items) != 2 || items[0].ID != "one" || items[1].ID != "two" {
		t.Errorf("items = %#v", items)
	}
}

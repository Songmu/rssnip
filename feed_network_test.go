// This file covers network-level behavior of feed fetching: error
// propagation, cancellation, and redirect handling.
package rssnip

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/idna"
)

type errorTransport struct{ err error }

func (transport errorTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, transport.err
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (roundTrip roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

type recordingTransport struct {
	mu    sync.Mutex
	start []time.Time
	body  string
}

func (transport *recordingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	transport.mu.Lock()
	transport.start = append(transport.start, time.Now())
	transport.mu.Unlock()
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(transport.body)),
		Header:     make(http.Header),
	}, nil
}

func (transport *recordingTransport) starts() []time.Time {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	return append([]time.Time(nil), transport.start...)
}

func TestPoliteTransportSpacesRequestsToSameHost(t *testing.T) {
	transport := &recordingTransport{}
	client := &http.Client{Transport: newPoliteTransport(transport)}
	for _, path := range []string{"/one", "/two"} {
		response, err := client.Get("https://example.com" + path)
		if err != nil {
			t.Fatal(err)
		}
		if err := response.Body.Close(); err != nil {
			t.Fatal(err)
		}
	}
	starts := transport.starts()
	if got := starts[1].Sub(starts[0]); got < hostFetchInterval {
		t.Errorf("request interval = %s, want at least %s", got, hostFetchInterval)
	}
}

func TestPoliteTransportSerializesConcurrentCanonicalHosts(t *testing.T) {
	secondStarted := make(chan time.Time, 1)
	secondDeadline := make(chan time.Time, 1)
	transport := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/two" {
			secondStarted <- time.Now()
			deadline, ok := request.Context().Deadline()
			if !ok {
				t.Error("second request has no timeout")
			}
			secondDeadline <- deadline
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     make(http.Header),
		}, nil
	})
	client := &http.Client{Transport: newPoliteTransport(transport)}
	first, err := client.Get("https://EXAMPLE.com/one")
	if err != nil {
		t.Fatal(err)
	}
	type responseResult struct {
		response *http.Response
		err      error
	}
	done := make(chan responseResult, 1)
	go func() {
		response, err := client.Get("https://example.com./two")
		done <- responseResult{response: response, err: err}
	}()
	select {
	case <-secondStarted:
		t.Fatal("second request started before the first response closed")
	case <-time.After(50 * time.Millisecond):
	}
	closedAt := time.Now()
	if err := first.Body.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case startedAt := <-secondStarted:
		if got := startedAt.Sub(closedAt); got < hostFetchInterval {
			t.Errorf("second request started after %s, want at least %s", got, hostFetchInterval)
		}
		deadline := <-secondDeadline
		if timeout := deadline.Sub(closedAt); timeout < hostFetchInterval+feedRequestTimeout-100*time.Millisecond {
			t.Errorf("second request timeout after close = %s, want approximately %s", timeout, hostFetchInterval+feedRequestTimeout)
		}
	case <-time.After(2 * hostFetchInterval):
		t.Fatal("second request did not start")
	}
	result := <-done
	if result.err != nil {
		t.Fatal(result.err)
	}
	if err := result.response.Body.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCanonicalHost(t *testing.T) {
	tests := []struct {
		host    string
		want    string
		invalid bool
	}{
		{host: "EXAMPLE.com.", want: "example.com"},
		{host: "bücher.example", want: "xn--bcher-kva.example"},
		{host: "xn--bcher-kva.example", want: "xn--bcher-kva.example"},
		{host: "example.com。", want: "example.com"},
		{host: "example.com．", want: "example.com"},
		{host: "example.com｡", want: "example.com"},
		{host: "foo_bar.example", want: "foo_bar.example", invalid: true},
		{host: "192.0.2.1", want: "192.0.2.1"},
		{host: "2001:DB8::0:1", want: "2001:db8::1"},
		{host: "fe80::1%en0", want: "fe80::1%en0"},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			if tt.invalid {
				if _, err := idna.Lookup.ToASCII(tt.host); err == nil {
					t.Fatalf("idna.Lookup.ToASCII(%q) succeeded, want error", tt.host)
				}
			}
			if got := canonicalHost(tt.host); got != tt.want {
				t.Errorf("canonicalHost(%q) = %q, want %q", tt.host, got, tt.want)
			}
		})
	}
}

func TestPoliteTransportPacesPagination(t *testing.T) {
	var mu sync.Mutex
	var starts []time.Time
	transport := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		mu.Lock()
		starts = append(starts, time.Now())
		mu.Unlock()
		body := `{"version":"https://jsonfeed.org/version/1.1","items":[]}`
		if request.URL.Path == "/one" {
			body = `{"version":"https://jsonfeed.org/version/1.1","next_url":"/two","items":[]}`
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})
	client := &http.Client{Transport: newPoliteTransport(transport)}
	if _, err := fetchFeedPages(context.Background(), client, "https://example.com/one", 2); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if got := starts[1].Sub(starts[0]); got < hostFetchInterval {
		t.Errorf("pagination interval = %s, want at least %s", got, hostFetchInterval)
	}
}

func TestFetchFeedsLimitsConcurrentHosts(t *testing.T) {
	const feed = `{"version":"https://jsonfeed.org/version/1.1","items":[]}`
	started := make(chan struct{}, maxConcurrentFetches)
	release := make(chan struct{})
	var mu sync.Mutex
	inFlight, maximum := 0, 0
	transport := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		mu.Lock()
		inFlight++
		if inFlight > maximum {
			maximum = inFlight
		}
		mu.Unlock()
		started <- struct{}{}
		<-release
		mu.Lock()
		inFlight--
		mu.Unlock()
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(feed)),
			Header:     make(http.Header),
		}, nil
	})
	urls := make([]string, maxConcurrentFetches+1)
	for index := range urls {
		urls[index] = (&url.URL{Scheme: "https", Host: fmt.Sprintf("host-%d.example", index)}).String()
	}
	done := make(chan error, 1)
	go func() {
		done <- fetchFeeds(context.Background(), &http.Client{Transport: transport}, urls, 1, nil, false,
			func(_ []Item, err error) error { return err })
	}()
	for range maxConcurrentFetches {
		<-started
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if maximum != maxConcurrentFetches {
		t.Errorf("maximum concurrent requests = %d, want %d", maximum, maxConcurrentFetches)
	}
}

func TestFetchFeedsBackpressuresCompletedFeeds(t *testing.T) {
	const feed = `{"version":"https://jsonfeed.org/version/1.1","items":[]}`
	started := make(chan struct{}, maxConcurrentFetches+1)
	releaseFirst := make(chan struct{})
	transport := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		started <- struct{}{}
		if request.URL.Path == "/0" {
			<-releaseFirst
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(feed)),
			Header:     make(http.Header),
		}, nil
	})
	urls := make([]string, maxConcurrentFetches+1)
	for index := range urls {
		urls[index] = fmt.Sprintf("https://host-%d.example/%d", index, index)
	}
	done := make(chan error, 1)
	go func() {
		done <- fetchFeeds(context.Background(), &http.Client{Transport: transport}, urls, 1, nil, false,
			func(_ []Item, err error) error { return err })
	}()
	for range maxConcurrentFetches {
		<-started
	}
	select {
	case <-started:
		t.Fatal("started a request beyond the completed-feed window")
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseFirst)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRunWritesFeedsInInputOrder(t *testing.T) {
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(firstStarted)
		<-releaseFirst
		fmt.Fprint(w, `{"version":"https://jsonfeed.org/version/1.1","items":[{"id":"first","title":"First"}]}`)
	}))
	t.Cleanup(first.Close)
	secondFinished := make(chan struct{})
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"version":"https://jsonfeed.org/version/1.1","items":[{"id":"second","title":"Second"}]}`)
		close(secondFinished)
	}))
	t.Cleanup(second.Close)

	secondURL := strings.Replace(second.URL, "127.0.0.1", "localhost", 1)
	var stdout, stderr strings.Builder
	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), []string{"--all", first.URL, secondURL}, &stdout, &stderr)
	}()
	<-firstStarted
	select {
	case <-secondFinished:
	case <-time.After(time.Second):
		t.Fatal("second feed did not complete before the first")
	}
	close(releaseFirst)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	firstIndex := strings.Index(stdout.String(), `"title":"First"`)
	secondIndex := strings.Index(stdout.String(), `"title":"Second"`)
	if firstIndex < 0 || secondIndex < 0 || firstIndex > secondIndex {
		t.Errorf("output is not in input order: %s", stdout.String())
	}
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
	err := Run(ctx, []string{"--all", server.URL}, &stdout, &stderr)
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

func TestFetchFeedPagesStopsWhenSinceOrderIsExhausted(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		pages         []string
		preferUpdated bool
		wantRequests  int
		wantItems     int
	}{
		{
			name: "published order",
			pages: []string{
				`[{"id":"one","date_published":"2024-03-05T00:00:00Z"},{"id":"two","date_published":"2024-03-04T00:00:00Z"},{"id":"three","date_published":"2024-03-03T00:00:00Z"}]`,
				`[{"id":"four","date_published":"2024-03-01T00:00:00Z"},{"id":"five","date_published":"2024-02-28T00:00:00Z"}]`,
				`[{"id":"six","date_published":"2024-02-27T00:00:00Z"}]`,
			},
			wantRequests: 2,
			wantItems:    5,
		},
		{
			name: "modified order bounds published filtering",
			pages: []string{
				`[{"id":"one","date_published":"2024-02-20T00:00:00Z","date_modified":"2024-03-03T00:00:00Z"},{"id":"two","date_published":"2024-02-19T00:00:00Z","date_modified":"2024-03-02T00:00:00Z"}]`,
				`[{"id":"three","date_published":"2024-02-25T00:00:00Z","date_modified":"2024-02-28T00:00:00Z"},{"id":"four","date_published":"2024-02-18T00:00:00Z","date_modified":"2024-02-27T00:00:00Z"},{"id":"five","date_published":"2024-02-17T00:00:00Z","date_modified":"2024-02-26T00:00:00Z"}]`,
				`[{"id":"six","date_published":"2024-01-10T00:00:00Z","date_modified":"2024-02-25T00:00:00Z"}]`,
			},
			wantRequests: 2,
			wantItems:    5,
		},
		{
			name: "modified order with updated filtering",
			pages: []string{
				`[{"id":"one","date_published":"2024-01-01T00:00:00Z","date_modified":"2024-03-03T00:00:00Z"},{"id":"two","date_published":"2024-02-20T00:00:00Z","date_modified":"2024-03-02T00:00:00Z"}]`,
				`[{"id":"three","date_published":"2024-01-15T00:00:00Z","date_modified":"2024-02-28T00:00:00Z"},{"id":"four","date_published":"2024-01-14T00:00:00Z","date_modified":"2024-02-27T00:00:00Z"},{"id":"five","date_published":"2024-01-13T00:00:00Z","date_modified":"2024-02-26T00:00:00Z"}]`,
				`[{"id":"six","date_published":"2024-01-10T00:00:00Z","date_modified":"2024-02-25T00:00:00Z"}]`,
			},
			preferUpdated: true,
			wantRequests:  2,
			wantItems:     5,
		},
		{
			name: "published order cannot terminate updated filtering",
			pages: []string{
				`[{"id":"one","date_published":"2024-03-03T00:00:00Z","date_modified":"2024-02-01T00:00:00Z"},{"id":"two","date_published":"2024-03-02T00:00:00Z","date_modified":"2024-02-03T00:00:00Z"}]`,
				`[{"id":"three","date_published":"2024-02-28T00:00:00Z","date_modified":"2024-02-02T00:00:00Z"}]`,
				`[{"id":"four","date_published":"2024-02-27T00:00:00Z","date_modified":"2024-03-04T00:00:00Z"}]`,
			},
			preferUpdated: true,
			wantRequests:  3,
			wantItems:     4,
		},
		{
			name: "invalid modified bound continues ordinary filtering",
			pages: []string{
				`[{"id":"one","date_published":"2024-02-20T00:00:00Z","date_modified":"2024-03-03T00:00:00Z"},{"id":"two","date_published":"2024-02-19T00:00:00Z","date_modified":"2024-03-02T00:00:00Z"}]`,
				`[{"id":"three","date_published":"2024-02-25T00:00:00Z","date_modified":"2024-02-28T00:00:00Z"},{"id":"four","date_published":"2024-02-27T00:00:00Z","date_modified":"2024-02-26T00:00:00Z"}]`,
				`[{"id":"five","date_published":"2024-02-18T00:00:00Z","date_modified":"2024-02-25T00:00:00Z"}]`,
			},
			wantRequests: 3,
			wantItems:    5,
		},
		{
			name: "missing dates do not reach threshold",
			pages: []string{
				`[{"id":"one","date_published":"2024-03-03T00:00:00Z"},{"id":"missing"}]`,
				`[{"id":"two","date_published":"2024-02-28T00:00:00Z"},{"id":"also-missing"}]`,
				`[{"id":"three","date_published":"2024-02-27T00:00:00Z"}]`,
			},
			wantRequests: 3,
			wantItems:    5,
		},
		{
			name: "duplicate IDs still provide ordering evidence",
			pages: []string{
				`[{"id":"one","date_published":"2024-03-05T00:00:00Z"},{"id":"duplicate","date_published":"2024-03-04T00:00:00Z"}]`,
				`[{"id":"duplicate","date_published":"2024-03-03T00:00:00Z"},{"id":"duplicate","date_published":"2024-03-02T00:00:00Z"},{"id":"duplicate","date_published":"2024-02-28T00:00:00Z"}]`,
				`[{"id":"three","date_published":"2024-02-27T00:00:00Z"}]`,
			},
			wantRequests: 2,
			wantItems:    2,
		},
		{
			name: "since equality continues pagination",
			pages: []string{
				`[{"id":"one","date_published":"2024-03-05T00:00:00Z"},{"id":"two","date_published":"2024-03-04T00:00:00Z"},{"id":"three","date_published":"2024-03-03T00:00:00Z"}]`,
				`[{"id":"four","date_published":"2024-03-02T00:00:00Z"},{"id":"five","date_published":"2024-03-01T00:00:00Z"}]`,
				`[{"id":"six","date_published":"2024-02-28T00:00:00Z"}]`,
			},
			wantRequests: 3,
			wantItems:    6,
		},
		{
			name: "later item on threshold page rejects ordering",
			pages: []string{
				`[{"id":"one","date_published":"2024-03-05T00:00:00Z"},{"id":"two","date_published":"2024-03-04T00:00:00Z"}]`,
				`[{"id":"three","date_published":"2024-03-03T00:00:00Z"},{"id":"four","date_published":"2024-03-02T00:00:00Z"},{"id":"five","date_published":"2024-02-28T00:00:00Z"},{"id":"six","date_published":"2024-03-06T00:00:00Z"}]`,
				`[{"id":"seven","date_published":"2024-02-27T00:00:00Z"}]`,
			},
			wantRequests: 3,
			wantItems:    7,
		},
		{
			name: "rejected candidates continue to max pages",
			pages: []string{
				`[{"id":"one","date_published":"2024-03-03T00:00:00Z","date_modified":"2024-03-03T00:00:00Z"},{"id":"two","date_published":"2024-02-28T00:00:00Z","date_modified":"2024-02-28T00:00:00Z"}]`,
				`[{"id":"three","date_published":"2024-03-02T00:00:00Z","date_modified":"2024-03-02T00:00:00Z"}]`,
				`[{"id":"four","date_published":"2024-02-27T00:00:00Z","date_modified":"2024-02-27T00:00:00Z"}]`,
			},
			wantRequests: 3,
			wantItems:    4,
		},
	}

	since := mustParseTimeBound(t, "2024-03-01")
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var requests int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				page := requests - 1
				if page >= len(tt.pages) {
					http.NotFound(w, r)
					return
				}
				nextURL := ""
				if page+1 < len(tt.pages) {
					nextURL = fmt.Sprintf(`,"next_url":"/page/%d"`, page+2)
				}
				fmt.Fprintf(w,
					`{"version":"https://jsonfeed.org/version/1.1"%s,"items":%s}`,
					nextURL, tt.pages[page])
			}))
			t.Cleanup(server.Close)

			items, err := fetchFeedPagesSince(
				context.Background(), server.Client(), server.URL+"/page/1",
				len(tt.pages), since, tt.preferUpdated)
			if err != nil {
				t.Fatal(err)
			}
			if requests != tt.wantRequests {
				t.Errorf("requests = %d, want %d", requests, tt.wantRequests)
			}
			if len(items) != tt.wantItems {
				t.Errorf("items = %d, want %d: %#v", len(items), tt.wantItems, items)
			}
		})
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

func TestFetchFeedPagesFollowsWordPressPagination(t *testing.T) {
	t.Parallel()
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.RequestURI())
		if r.URL.Query().Get("category") != "go" {
			http.Error(w, "missing category", http.StatusBadRequest)
			return
		}
		switch r.URL.Query().Get("paged") {
		case "":
			w.Write([]byte(`<rss><channel><title>Feed</title><generator>https://wordpress.org/?v=7.1</generator><item><guid>one</guid></item><item><guid>duplicate</guid></item></channel></rss>`))
		case "2":
			w.Write([]byte(`<rss><channel><title>Feed</title><generator>https://wordpress.org/?v=7.1</generator><item><guid>duplicate</guid></item><item><guid>two</guid></item></channel></rss>`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	items, err := fetchFeedPages(context.Background(), server.Client(), server.URL+"/feed?category=go#part", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 || items[0].ID != "one" || items[1].ID != "duplicate" || items[2].ID != "two" {
		t.Fatalf("items = %#v", items)
	}
	wantRequests := []string{"/feed?category=go", "/feed?category=go&paged=2", "/feed?category=go&paged=3"}
	if strings.Join(requests, "\n") != strings.Join(wantRequests, "\n") {
		t.Errorf("requests = %v, want %v", requests, wantRequests)
	}
	if items[2].Feed.FeedURL != server.URL+"/feed?category=go&paged=2" {
		t.Errorf("second page feed URL = %q", items[2].Feed.FeedURL)
	}
}

func TestFetchFeedPagesBoundsWordPressPagination(t *testing.T) {
	t.Parallel()
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		page := r.URL.Query().Get("paged")
		if page == "" {
			page = "1"
		}
		w.Write([]byte(`<rss><channel><generator>https://wordpress.org/?v=7.1</generator><item><guid>` + page + `</guid></item></channel></rss>`))
	}))
	t.Cleanup(server.Close)

	items, err := fetchFeedPages(context.Background(), server.Client(), server.URL+"/feed", 2)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 || len(items) != 2 || items[0].ID != "1" || items[1].ID != "2" {
		t.Errorf("requests = %d, items = %#v", requests, items)
	}
}

func TestFetchFeedPagesWordPressTermination(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{"not found", http.StatusNotFound, ""},
		{"gone", http.StatusGone, ""},
		{"empty feed", http.StatusOK, `<rss><channel><generator>https://wordpress.org/?v=7.1</generator></channel></rss>`},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var requests int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.URL.Query().Get("paged") == "" {
					w.Write([]byte(`<rss><channel><generator>https://wordpress.org/?v=7.1</generator><item><guid>one</guid></item></channel></rss>`))
					return
				}
				w.WriteHeader(tt.status)
				w.Write([]byte(tt.body))
			}))
			t.Cleanup(server.Close)

			items, err := fetchFeedPages(context.Background(), server.Client(), server.URL+"/feed", 10)
			if err != nil {
				t.Fatal(err)
			}
			if requests != 2 || len(items) != 1 || items[0].ID != "one" {
				t.Errorf("requests = %d, items = %#v", requests, items)
			}
		})
	}
}

func TestFetchFeedPagesEmptyWordPressFeedDoesNotGuess(t *testing.T) {
	t.Parallel()
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Write([]byte(`<rss><channel><generator>https://wordpress.org/?v=7.1</generator></channel></rss>`))
	}))
	t.Cleanup(server.Close)

	items, err := fetchFeedPages(context.Background(), server.Client(), server.URL+"/feed", 10)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 || len(items) != 0 {
		t.Errorf("requests = %d, items = %#v", requests, items)
	}
}

func TestFetchFeedPagesStopsUnchangedWordPressPagination(t *testing.T) {
	t.Parallel()
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Write([]byte(`<rss><channel><generator>https://wordpress.org/?v=7.1</generator><item><guid>one</guid></item><item><guid>two</guid></item></channel></rss>`))
	}))
	t.Cleanup(server.Close)

	items, err := fetchFeedPages(context.Background(), server.Client(), server.URL+"/feed", 10)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Errorf("requests = %d, want 2", requests)
	}
	if len(items) != 2 || items[0].ID != "one" || items[1].ID != "two" {
		t.Errorf("items = %#v", items)
	}
}

func TestFetchFeedPagesWordPressFallbackPreservesErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"server error", http.StatusInternalServerError, "", "500 Internal Server Error"},
		{"malformed feed", http.StatusOK, "not a feed", "parse feed"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("paged") == "" {
					w.Write([]byte(`<rss><channel><generator>https://wordpress.org/?v=7.1</generator><item><guid>one</guid></item></channel></rss>`))
					return
				}
				w.WriteHeader(tt.status)
				w.Write([]byte(tt.body))
			}))
			t.Cleanup(server.Close)

			items, err := fetchFeedPages(context.Background(), server.Client(), server.URL+"/feed", 10)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("items = %#v, error = %v, want error containing %q", items, err, tt.want)
			}
			if items != nil {
				t.Errorf("items = %#v, want nil on error", items)
			}
		})
	}
}

func TestFetchFeedPagesWordPressExplicitPaginationTakesPrecedence(t *testing.T) {
	t.Parallel()
	var guessedRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("paged") {
			guessedRequests++
		}
		switch r.URL.Path {
		case "/one":
			w.Write([]byte(`<rss xmlns:atom="http://www.w3.org/2005/Atom"><channel><generator>https://wordpress.org/?v=7.1</generator><atom:link rel="next" href="/two"/><item><guid>one</guid></item></channel></rss>`))
		case "/two":
			w.Write([]byte(`<rss><channel><generator>https://wordpress.org/?v=7.1</generator><item><guid>two</guid></item></channel></rss>`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	items, err := fetchFeedPages(context.Background(), server.Client(), server.URL+"/one", 10)
	if err != nil {
		t.Fatal(err)
	}
	if guessedRequests != 0 || len(items) != 2 || items[0].ID != "one" || items[1].ID != "two" {
		t.Errorf("guessed requests = %d, items = %#v", guessedRequests, items)
	}
}

func TestFetchFeedPagesDoesNotGuessForNonWordPressFeeds(t *testing.T) {
	t.Parallel()
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Write([]byte(`<rss><channel><generator>https://example.com/generator</generator><item><guid>one</guid></item></channel></rss>`))
	}))
	t.Cleanup(server.Close)

	items, err := fetchFeedPages(context.Background(), server.Client(), server.URL+"/feed", 10)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 || len(items) != 1 || items[0].ID != "one" {
		t.Errorf("requests = %d, items = %#v", requests, items)
	}
}

func TestFetchFeedPagesWordPressFallbackUsesDiscoveredRedirectURL(t *testing.T) {
	t.Parallel()
	var pagedRequest string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/blog":
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(`<html><head><link rel="feed" href="/redirect"></head></html>`))
		case "/redirect":
			http.Redirect(w, r, "/feed?category=go", http.StatusFound)
		case "/feed":
			switch r.URL.Query().Get("paged") {
			case "":
				w.Write([]byte(`<rss><channel><generator>https://wordpress.org/?v=7.1</generator><item><guid>one</guid></item></channel></rss>`))
			case "2":
				pagedRequest = r.URL.RequestURI()
				w.Write([]byte(`<rss><channel><generator>https://wordpress.org/?v=7.1</generator><item><guid>two</guid></item></channel></rss>`))
			default:
				http.NotFound(w, r)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	items, err := fetchFeedPages(context.Background(), server.Client(), server.URL+"/blog", 10)
	if err != nil {
		t.Fatal(err)
	}
	if pagedRequest != "/feed?category=go&paged=2" {
		t.Errorf("paged request = %q", pagedRequest)
	}
	if len(items) != 2 || items[0].ID != "one" || items[1].ID != "two" {
		t.Errorf("items = %#v", items)
	}
}

func TestFetchFeedPagesWordPressRedirectKeepsGuessedPageNumber(t *testing.T) {
	t.Parallel()
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.RequestURI())
		switch {
		case r.URL.Path == "/feed" && r.URL.Query().Get("paged") == "":
			w.Write([]byte(`<rss><channel><generator>https://wordpress.org/?v=7.1</generator><item><guid>one</guid></item></channel></rss>`))
		case r.URL.Path == "/feed" && r.URL.Query().Get("paged") == "2":
			http.Redirect(w, r, "/canonical", http.StatusFound)
		case r.URL.Path == "/canonical" && r.URL.Query().Get("paged") == "":
			w.Write([]byte(`<rss><channel><generator>https://wordpress.org/?v=7.1</generator><item><guid>two</guid></item></channel></rss>`))
		case r.URL.Path == "/canonical" && r.URL.Query().Get("paged") == "3":
			w.Write([]byte(`<rss><channel><generator>https://wordpress.org/?v=7.1</generator><item><guid>three</guid></item></channel></rss>`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	items, err := fetchFeedPages(context.Background(), server.Client(), server.URL+"/feed", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 || items[0].ID != "one" || items[1].ID != "two" || items[2].ID != "three" {
		t.Fatalf("items = %#v", items)
	}
	wantRequests := []string{"/feed", "/feed?paged=2", "/canonical", "/canonical?paged=3", "/canonical?paged=4"}
	if strings.Join(requests, "\n") != strings.Join(wantRequests, "\n") {
		t.Errorf("requests = %v, want %v", requests, wantRequests)
	}
}

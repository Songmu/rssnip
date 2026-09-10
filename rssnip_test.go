package rssnip

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"
)

func TestRunFiltersAndAppliesRawJQ(t *testing.T) {
	t.Parallel()
	server := newFeedServer(t, string(mustReadTestdata(t, "sample_rss.xml")))
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"--since", "2024-01-01",
		"--until", "2024-01-31",
		"--jq", ".url",
		"-r",
		server.URL,
	}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	want := "https://example.com/first\nhttps://example.com/second\n"
	if got := stdout.String(); got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestRunDiscoversFeedFromBlogURL(t *testing.T) {
	t.Parallel()
	testRSS := string(mustReadTestdata(t, "sample_rss.xml"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); !strings.HasPrefix(got, "rssnip/") {
			t.Errorf("User-Agent = %q", got)
		}
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, `<html><head><link rel="alternate" type="application/rss+xml" href="/feeds/index.xml"></head></html>`)
		case "/feeds/index.xml":
			w.Header().Set("Content-Type", "application/rss+xml")
			fmt.Fprint(w, testRSS)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"--with-feed", "--jq", "._feed.feed_url", "-r", server.URL}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Repeat(server.URL+"/feeds/index.xml\n", 4)
	if got := stdout.String(); got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestRunDiscoversFeedFromBlogURLWithBaseHref(t *testing.T) {
	t.Parallel()
	testRSS := string(mustReadTestdata(t, "sample_rss.xml"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/blog/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, `<html><head><link rel="alternate" type="application/rss+xml" href="feed.xml"><base href="/assets/"></head></html>`)
		case "/assets/feed.xml":
			w.Header().Set("Content-Type", "application/rss+xml")
			fmt.Fprint(w, testRSS)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"--with-feed", "--jq", "._feed.feed_url", "-r", server.URL + "/blog/"}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Repeat(server.URL+"/assets/feed.xml\n", 4)
	if got := stdout.String(); got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestRunDiscoversFeedFromRelFeedLink(t *testing.T) {
	t.Parallel()
	testRSS := string(mustReadTestdata(t, "sample_rss.xml"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, `<html><head><link rel="feed" href="/rss"></head></html>`)
		case "/rss":
			w.Header().Set("Content-Type", "application/rss+xml")
			fmt.Fprint(w, testRSS)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"--with-feed", "--jq", "._feed.feed_url", "-r", server.URL}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Repeat(server.URL+"/rss\n", 4)
	if got := stdout.String(); got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestRunDiscoversFeedFromBOMPrefixedHTML(t *testing.T) {
	t.Parallel()
	testRSS := string(mustReadTestdata(t, "sample_rss.xml"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			fmt.Fprint(w, "\xef\xbb\xbf\n  <!-- generated -->\n<html><head><link rel=\"feed\" href=\"/rss\"></head></html>")
		case "/rss":
			w.Header().Set("Content-Type", "application/rss+xml")
			fmt.Fprint(w, testRSS)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"--with-feed", "--jq", "._feed.feed_url", "-r", server.URL}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Repeat(server.URL+"/rss\n", 4)
	if got := stdout.String(); got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestRunDiscoversFeedFromNonUTF8HTML(t *testing.T) {
	t.Parallel()
	testRSS := string(mustReadTestdata(t, "sample_rss.xml"))
	// "café" encoded as ISO-8859-1 (Latin-1), where the "é" is the single
	// byte 0xE9 rather than its two-byte UTF-8 encoding.
	page := []byte("<html><head><base href=\"/blog/caf\xe9/\"><link rel=\"feed\" href=\"feed.xml\"></head></html>")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/blog/":
			w.Header().Set("Content-Type", "text/html; charset=iso-8859-1")
			w.Write(page)
		case "/blog/café/feed.xml":
			w.Header().Set("Content-Type", "application/rss+xml")
			fmt.Fprint(w, testRSS)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"--with-feed", "--jq", "._feed.feed_url", "-r", server.URL + "/blog/"}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Repeat(server.URL+"/blog/caf%C3%A9/feed.xml\n", 4)
	if got := stdout.String(); got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestRunAcceptsURLInputs(t *testing.T) {
	t.Parallel()
	testRSS := string(mustReadTestdata(t, "sample_rss.xml"))
	server1 := newFeedServer(t, strings.Replace(testRSS, "Example Feed", "Feed One", 1))
	server2 := newFeedServer(t, strings.Replace(testRSS, "Example Feed", "Feed Two", 1))
	server3 := newFeedServer(t, strings.Replace(testRSS, "Example Feed", "Feed Three", 1))
	for _, tt := range []struct {
		name  string
		urls  []string
		stdin string
		feeds []string
	}{
		{
			name:  "positional only",
			urls:  []string{server1.URL, server2.URL},
			feeds: []string{"Feed One", "Feed Two"},
		},
		{
			name:  "stdin only",
			stdin: "\r\n \t" + server2.URL + " \r\n\n" + server1.URL + "\n",
			feeds: []string{"Feed Two", "Feed One"},
		},
		{
			name:  "positional then stdin with duplicates",
			urls:  []string{server1.URL, server2.URL},
			stdin: "\n" + server3.URL + "\n" + server1.URL + "\n",
			feeds: []string{"Feed One", "Feed Two", "Feed Three", "Feed One"},
		},
		{
			name:  "option terminator",
			urls:  []string{"--", server1.URL},
			feeds: []string{"Feed One"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			args := append([]string{"--with-feed", "--jq", "._feed.title", "-r"}, tt.urls...)
			var stdout, stderr bytes.Buffer
			if err := run(context.Background(), args, strings.NewReader(tt.stdin), &stdout, &stderr); err != nil {
				t.Fatal(err)
			}
			var want []string
			for _, feed := range tt.feeds {
				for range 4 {
					want = append(want, feed)
				}
			}
			values := strings.Split(strings.TrimSpace(stdout.String()), "\n")
			if !reflect.DeepEqual(values, want) {
				t.Errorf("values = %#v, want %#v", values, want)
			}
			if stderr.Len() != 0 {
				t.Errorf("stderr = %q", stderr.String())
			}
		})
	}
}

func TestRunJQFeedMetadataIsOptIn(t *testing.T) {
	t.Parallel()
	server := newFeedServer(t, string(mustReadTestdata(t, "sample_rss.xml")))
	for _, tt := range []struct {
		name     string
		withFeed bool
		want     string
	}{
		{name: "omitted by default", want: "false\n"},
		{name: "included with flag", withFeed: true, want: "true\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			args := []string{"--jq", `if .id == "before" then has("_feed") else empty end`}
			if tt.withFeed {
				args = append(args, "--with-feed")
			}
			args = append(args, server.URL)
			if err := Run(context.Background(), args, &stdout, &stderr); err != nil {
				t.Fatal(err)
			}
			if got := stdout.String(); got != tt.want {
				t.Errorf("stdout = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRunPreservesMultipleFeedOrder(t *testing.T) {
	t.Parallel()
	testRSS := string(mustReadTestdata(t, "sample_rss.xml"))
	firstServer := newFeedServer(t, testRSS)
	secondServer := newFeedServer(t, strings.Replace(testRSS, "<guid>before</guid>", "<guid>other</guid>", 1))
	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{"--with-feed", firstServer.URL, secondServer.URL}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 8 {
		t.Fatalf("got %d lines, want 8", len(lines))
	}
	var first, second Item
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(lines[4]), &second); err != nil {
		t.Fatal(err)
	}
	if first.ID != "before" || first.Feed.FeedURL != firstServer.URL {
		t.Errorf("first item = %#v", first)
	}
	if second.ID != "other" || second.Feed.FeedURL != secondServer.URL {
		t.Errorf("second feed item = %#v", second)
	}
}

func TestRunJQCanEmitZeroOrMultipleValues(t *testing.T) {
	t.Parallel()
	server := newFeedServer(t, string(mustReadTestdata(t, "sample_rss.xml")))
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"--jq", `if .id == "first" then empty elif .id == "second" then [.id, "second-extra"][] else empty end`,
		"-r",
		server.URL,
	}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	want := "second\nsecond-extra\n"
	if got := stdout.String(); got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestRunStreamsJSONLinesBeforeLaterFeedFailure(t *testing.T) {
	t.Parallel()
	goodServer := newFeedServer(t, string(mustReadTestdata(t, "sample_rss.xml")))
	badServer := newBadGatewayServer(t)
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{goodServer.URL, badServer.URL}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "502 Bad Gateway") {
		t.Fatalf("error = %v", err)
	}
	if lines := strings.Count(strings.TrimSpace(stdout.String()), "\n") + 1; lines != 4 {
		t.Errorf("streamed lines = %d, want 4", lines)
	}
}

func TestRunWritesJSONLinesWithoutFeedMetadataByDefault(t *testing.T) {
	t.Parallel()
	server := newFeedServer(t, string(mustReadTestdata(t, "sample_rss.xml")))
	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{server.URL}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4", len(lines))
	}
	var item Item
	if err := json.Unmarshal([]byte(lines[0]), &item); err != nil {
		t.Fatal(err)
	}
	if item.ID != "before" || item.Feed.FeedURL != "" {
		t.Errorf("unexpected item: %#v", item)
	}
}

func TestRunRejectsRemovedURLOption(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("unexpected HTTP request")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	for _, args := range [][]string{
		{"--url", server.URL},
		{"--url=" + server.URL},
		{"-url", server.URL},
	} {
		t.Run(args[0], func(t *testing.T) {
			t.Parallel()
			stdin := strings.NewReader(server.URL + "\n")
			var stdout, stderr bytes.Buffer
			err := run(context.Background(), args, stdin, &stdout, &stderr)
			const want = "flag provided but not defined: -url"
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("error = %v, want substring %q", err, want)
			}
			if !strings.Contains(stderr.String(), want) {
				t.Errorf("stderr = %q, want substring %q", stderr.String(), want)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}
			if stdin.Len() != len(server.URL)+1 {
				t.Error("standard input was read before rejecting the option")
			}
		})
	}
}

func TestRunStdinErrors(t *testing.T) {
	t.Parallel()
	cause := errors.New("stdin read failed")
	for _, tt := range []struct {
		name  string
		stdin io.Reader
		want  string
		cause error
	}{
		{
			name:  "empty input",
			stdin: strings.NewReader(""),
			want:  "at least one feed or blog/site URL is required",
		},
		{
			name:  "whitespace only",
			stdin: strings.NewReader(" \t\r\n\n"),
			want:  "at least one feed or blog/site URL is required",
		},
		{
			name:  "read failure",
			stdin: iotest.ErrReader(cause),
			want:  "read feed or blog/site URLs from standard input",
			cause: cause,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			err := run(context.Background(), nil, tt.stdin, &stdout, &stderr)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
			if tt.cause != nil && !errors.Is(err, tt.cause) {
				t.Errorf("error = %v, want wrapped cause %v", err, tt.cause)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}
		})
	}
}

func TestRunPrefersPublishedDateUnlessUpdatedRequested(t *testing.T) {
	t.Parallel()
	server := newFeedServer(t, string(mustReadTestdata(t, "atom_published_updated.xml")))
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "published by default",
			args: []string{"--since", "2024-01-01", "--until", "2024-01-31", "--jq", ".id", "-r", server.URL},
			want: "published-in-range\nupdated-only\n",
		},
		{
			name: "updated when requested",
			args: []string{"--since", "2024-01-01", "--until", "2024-01-31", "--updated", "--jq", ".id", "-r", server.URL},
			want: "updated-in-range\nupdated-only\n",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			if err := Run(context.Background(), tt.args, &stdout, &stderr); err != nil {
				t.Fatal(err)
			}
			if got := stdout.String(); got != tt.want {
				t.Errorf("stdout = %q, want %q", got, tt.want)
			}
			if stderr.Len() != 0 {
				t.Errorf("stderr = %q", stderr.String())
			}
		})
	}
}

func TestRunSinceStopsOrderedPagination(t *testing.T) {
	t.Parallel()
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		switch r.URL.Path {
		case "/one":
			fmt.Fprint(w, `{"version":"https://jsonfeed.org/version/1.1","next_url":"/two","items":[{"id":"one","date_published":"2024-03-05T00:00:00Z"},{"id":"two","date_published":"2024-03-04T00:00:00Z"},{"id":"three","date_published":"2024-03-03T00:00:00Z"}]}`)
		case "/two":
			fmt.Fprint(w, `{"version":"https://jsonfeed.org/version/1.1","next_url":"/three","items":[{"id":"four","date_published":"2024-03-01T00:00:00Z"},{"id":"old","date_published":"2024-02-28T00:00:00Z"}]}`)
		case "/three":
			fmt.Fprint(w, `{"version":"https://jsonfeed.org/version/1.1","items":[{"id":"unrequested","date_published":"2024-02-27T00:00:00Z"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"--since", "2024-03-01",
		"--jq", ".id",
		"-r",
		server.URL + "/one",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Errorf("requests = %d, want 2", requests)
	}
	if got, want := stdout.String(), "one\ntwo\nthree\nfour\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestRunErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"missing URL", nil, "at least one feed or blog/site URL is required"},
		{"raw without jq", []string{"-r", "https://example.com/feed"}, "-r requires --jq"},
		{"removed JSON option", []string{"--json", "https://example.com/feed"}, "flag provided but not defined: -json"},
		{"invalid since", []string{"--since", "yesterday", "https://example.com/feed"}, "invalid --since"},
		{"reversed period", []string{"--since", "2024-02-01", "--until", "2024-01-01", "https://example.com/feed"}, "--since must not be after --until"},
		{"updated without period", []string{"--updated", "https://example.com/feed"}, "--updated requires --since or --until"},
		{"invalid URL", []string{"--jq", ".", "://bad"}, "invalid feed URL"},
		{"relative URL", []string{"feed.xml"}, "invalid feed URL"},
		{"non-HTTP URL", []string{"ftp://example.com/feed"}, "invalid feed URL"},
		{"credential URL", []string{"http://user:" + "password@example.com/feed"}, "userinfo is not allowed"},
		{"zero max pages", []string{"--max-pages", "0", "https://example.com/feed"}, "--max-pages must be at least 1"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			err := Run(context.Background(), tt.args, &stdout, &stderr)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
			if strings.Contains(err.Error(), "password") {
				t.Fatalf("error exposes credentials: %v", err)
			}
		})
	}
}

func TestRunReportsHTTPError(t *testing.T) {
	t.Parallel()
	server := newBadGatewayServer(t)
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{server.URL}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "502 Bad Gateway") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunRejectsOversizedFeed(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(maxFeedSize+1))
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{server.URL}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "feed exceeds 32 MiB limit") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunReportsInvalidJQ(t *testing.T) {
	t.Parallel()
	server := newFeedServer(t, string(mustReadTestdata(t, "sample_rss.xml")))
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"--jq", "[", server.URL}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "parse --jq expression") {
		t.Fatalf("error = %v", err)
	}
}

package rssnip

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testRSS = `<?xml version="1.0"?>
<rss version="2.0">
  <channel>
    <title>Example Feed</title>
    <link>https://example.com/</link>
    <item>
      <guid>before</guid>
      <title>Before</title>
      <link>https://example.com/before</link>
      <pubDate>Sun, 31 Dec 2023 23:59:59 GMT</pubDate>
    </item>
    <item>
      <guid>first</guid>
      <title>First</title>
      <link>https://example.com/first</link>
      <pubDate>Mon, 01 Jan 2024 00:00:00 GMT</pubDate>
    </item>
    <item>
      <guid>second</guid>
      <title>Second</title>
      <link>https://example.com/second</link>
      <pubDate>Wed, 31 Jan 2024 23:59:59 GMT</pubDate>
    </item>
    <item>
      <guid>after</guid>
      <title>After</title>
      <link>https://example.com/after</link>
      <pubDate>Thu, 01 Feb 2024 00:00:00 GMT</pubDate>
    </item>
  </channel>
</rss>`

func TestRunFiltersAndAppliesRawJQ(t *testing.T) {
	t.Parallel()
	server := newFeedServer(t, testRSS)
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"--url", server.URL,
		"--since", "2024-01-01",
		"--until", "2024-01-31",
		"--jq", ".url",
		"-r",
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

func TestRunStreamsJSONLinesBeforeLaterFeedFailure(t *testing.T) {
	t.Parallel()
	goodServer := newFeedServer(t, testRSS)
	badServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no", http.StatusBadGateway)
	}))
	t.Cleanup(badServer.Close)
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{goodServer.URL, badServer.URL}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "502 Bad Gateway") {
		t.Fatalf("error = %v", err)
	}
	if lines := strings.Count(strings.TrimSpace(stdout.String()), "\n") + 1; lines != 4 {
		t.Errorf("streamed lines = %d, want 4", lines)
	}
}

func TestRunWritesJSONLinesByDefault(t *testing.T) {
	t.Parallel()
	server := newFeedServer(t, testRSS)
	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{"--with-feed", "--url", server.URL}, &stdout, &stderr); err != nil {
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
	if item.ID != "before" || item.Feed.FeedURL != server.URL {
		t.Errorf("unexpected item with feed metadata: %#v", item)
	}
}

func TestRunOmitsFeedMetadataByDefault(t *testing.T) {
	t.Parallel()
	server := newFeedServer(t, testRSS)
	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{"--url", server.URL}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout.String(), `"_feed"`) {
		t.Errorf("default output contains feed metadata: %s", stdout.String())
	}
}

func TestRunPreservesMultipleFeedOrder(t *testing.T) {
	t.Parallel()
	firstServer := newFeedServer(t, testRSS)
	secondServer := newFeedServer(t, strings.Replace(testRSS, "<guid>before</guid>", "<guid>other</guid>", 1))
	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{"--with-feed", "--url", firstServer.URL, "--url", secondServer.URL}, &stdout, &stderr); err != nil {
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
	server := newFeedServer(t, testRSS)
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"--url", server.URL,
		"--jq", `if .id == "first" then empty elif .id == "second" then [.id, "second-extra"][] else empty end`,
		"-r",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	want := "second\nsecond-extra\n"
	if got := stdout.String(); got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestRunErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"missing URL", nil, "at least one feed URL is required"},
		{"raw without jq", []string{"-r", "https://example.com/feed"}, "-r requires --jq"},
		{"removed JSON option", []string{"--json", "https://example.com/feed"}, "flag provided but not defined: -json"},
		{"invalid since", []string{"--since", "yesterday", "https://example.com/feed"}, "invalid --since"},
		{"reversed period", []string{"--since", "2024-02-01", "--until", "2024-01-01", "https://example.com/feed"}, "--since must not be after --until"},
		{"invalid URL", []string{"--jq", ".", "://bad"}, "invalid feed URL"},
		{"relative URL", []string{"feed.xml"}, "invalid feed URL"},
		{"non-HTTP URL", []string{"ftp://example.com/feed"}, "invalid feed URL"},
		{"credential URL", []string{"http://user:" + "password@example.com/feed"}, "userinfo is not allowed"},
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no", http.StatusBadGateway)
	}))
	t.Cleanup(server.Close)
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
	server := newFeedServer(t, testRSS)
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"--jq", "[", server.URL}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "parse --jq expression") {
		t.Fatalf("error = %v", err)
	}
}

func newFeedServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); !strings.HasPrefix(got, "rssnip/") {
			t.Errorf("User-Agent = %q", got)
		}
		accept := r.Header.Get("Accept")
		if !strings.Contains(accept, "application/json") || !strings.Contains(accept, "*/*") {
			t.Errorf("Accept = %q", accept)
		}
		w.Header().Set("Content-Type", "application/rss+xml")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(server.Close)
	return server
}

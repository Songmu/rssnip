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

func TestRunAcceptsMultipleURLsAndWritesJSONArray(t *testing.T) {
	t.Parallel()
	server1 := newFeedServer(t, strings.Replace(testRSS, "Example Feed", "Feed One", 1))
	server2 := newFeedServer(t, strings.Replace(testRSS, "Example Feed", "Feed Two", 1))
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"--json",
		"--jq", `{title: .title, feed: ._feed.title}`,
		"--url", server1.URL,
		server2.URL,
	}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	var values []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &values); err != nil {
		t.Fatalf("invalid JSON output: %v\n%s", err, stdout.String())
	}
	if len(values) != 8 {
		t.Fatalf("got %d values, want 8", len(values))
	}
	if got := values[0]["feed"]; got != "Feed One" {
		t.Errorf("first feed = %v", got)
	}
	if got := values[4]["feed"]; got != "Feed Two" {
		t.Errorf("second feed = %v", got)
	}
}

func TestRunWritesJSONLinesByDefault(t *testing.T) {
	t.Parallel()
	server := newFeedServer(t, testRSS)
	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{"--url", server.URL}, &stdout, &stderr); err != nil {
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
		t.Errorf("unexpected item: %#v", item)
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
		{"raw JSON", []string{"-r", "--json", "--jq", ".", "https://example.com/feed"}, "-r and --json"},
		{"invalid since", []string{"--since", "yesterday", "https://example.com/feed"}, "invalid --since"},
		{"reversed period", []string{"--since", "2024-02-01", "--until", "2024-01-01", "https://example.com/feed"}, "--since must not be after --until"},
		{"invalid jq", []string{"--jq", ".", "://bad"}, "invalid feed URL"},
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
		w.Header().Set("Content-Type", "application/rss+xml")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(server.Close)
	return server
}

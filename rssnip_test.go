package rssnip

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestRunFiltersAndAppliesRawJQ(t *testing.T) {
	t.Parallel()
	server := newFeedServer(t, string(mustReadTestdata(t, "sample_rss.xml")))
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

func TestRunAcceptsLocalFeedPathAndFileURL(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "feed.xml")
	if err := os.WriteFile(path, mustReadTestdata(t, "sample_rss.xml"), 0600); err != nil {
		t.Fatal(err)
	}

	fileURL := fileURLFromLocalPath(path)
	for _, feed := range []string{path, fileURL, strings.Replace(fileURL, "file:", "FILE:", 1)} {
		var stdout, stderr bytes.Buffer
		if err := Run(context.Background(), []string{feed}, &stdout, &stderr); err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
		if len(lines) != 4 {
			t.Fatalf("got %d lines for %q, want 4", len(lines), feed)
		}
	}
}

func TestRunAcceptsLocalPathWithInvalidURLSyntax(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("colon is not valid in a Windows filename")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "feed%.xml")
	if err := os.WriteFile(path, mustReadTestdata(t, "sample_rss.xml"), 0600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{path}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
}

func TestRunNormalizesUppercaseHTTPScheme(t *testing.T) {
	t.Parallel()
	server := newFeedServer(t, string(mustReadTestdata(t, "sample_rss.xml")))
	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{strings.ToUpper(server.URL[:5]) + server.URL[5:]}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
}

func TestRunLocalFeedResolvesRelativeURLs(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "feed.xml")
	if err := os.WriteFile(path, mustReadTestdata(t, "local_relative.xml"), 0600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{"--with-feed", path}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	var item Item
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout.String())), &item); err != nil {
		t.Fatal(err)
	}
	base := fileURLFromLocalPath(path)
	if item.Feed.FeedURL != base || item.URL != resolveURL(base, "article") ||
		len(item.Attachments) != 1 || item.Attachments[0].URL != resolveURL(base, "media.mp3") {
		t.Fatalf("item = %#v, want feed URL %q and relative URLs resolved", item, base)
	}
}

func TestRunAcceptsLocalPathWithDigitLeadingScheme(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "1:feed.xml")
	if err := os.WriteFile(path, mustReadTestdata(t, "sample_rss.xml"), 0600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{path}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
}

func TestRunAcceptsFileURLWithUppercaseLocalhost(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "feed.xml")
	if err := os.WriteFile(path, mustReadTestdata(t, "sample_rss.xml"), 0600); err != nil {
		t.Fatal(err)
	}
	fileURL := fileURLFromLocalPath(path)
	fileURL = strings.Replace(fileURL, "file://", "file://LOCALHOST", 1)
	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{fileURL}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
}

func TestRunAcceptsURLsFromStdinAndMergesSources(t *testing.T) {
	t.Parallel()
	testRSS := string(mustReadTestdata(t, "sample_rss.xml"))
	server1 := newFeedServer(t, strings.Replace(testRSS, "Example Feed", "Feed One", 1))
	server2 := newFeedServer(t, strings.Replace(testRSS, "Example Feed", "Feed Two", 1))
	server3 := newFeedServer(t, strings.Replace(testRSS, "Example Feed", "Feed Three", 1))
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{
		"--with-feed",
		"--jq", "._feed.title", "-r",
		"--url", server1.URL,
		"--url", server2.URL,
		server3.URL,
	}, strings.NewReader("\n"+server1.URL+"\n"), &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	values := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	want := []string{
		"Feed One", "Feed One", "Feed One", "Feed One",
		"Feed Two", "Feed Two", "Feed Two", "Feed Two",
		"Feed Three", "Feed Three", "Feed Three", "Feed Three",
		"Feed One", "Feed One", "Feed One", "Feed One",
	}
	if !reflect.DeepEqual(values, want) {
		t.Errorf("values = %#v, want %#v", values, want)
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
			args := []string{"--url", server.URL, "--jq", `if .id == "before" then has("_feed") else empty end`}
			if tt.withFeed {
				args = append(args, "--with-feed")
			}
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
	server := newFeedServer(t, string(mustReadTestdata(t, "sample_rss.xml")))
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
	if item.ID != "before" || item.Feed.FeedURL != "" {
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
		{"missing URL", nil, "at least one feed source is required"},
		{"raw without jq", []string{"-r", "https://example.com/feed"}, "-r requires --jq"},
		{"removed JSON option", []string{"--json", "https://example.com/feed"}, "flag provided but not defined: -json"},
		{"invalid since", []string{"--since", "yesterday", "https://example.com/feed"}, "invalid --since"},
		{"reversed period", []string{"--since", "2024-02-01", "--until", "2024-01-01", "https://example.com/feed"}, "--since must not be after --until"},
		{"invalid URL", []string{"--jq", ".", "://bad"}, "invalid feed URL"},
		{"missing local file", []string{"feed.xml"}, "stat feed"},
		{"non-HTTP URL", []string{"ftp://example.com/feed"}, "invalid feed URL"},
		{"credential URL", []string{"http://user:" + "password@example.com/feed"}, "userinfo is not allowed"},
		{"credential file URL", []string{"file://user:" + "password@localhost/feed.xml"}, "userinfo is not allowed"},
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

func TestRunRejectsOversizedLocalFeed(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "feed.xml")
	if err := os.WriteFile(path, make([]byte, maxFeedSize+1), 0600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{path}, &stdout, &stderr)
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

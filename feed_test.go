package rssnip

import (
	"strings"
	"testing"
)

func TestParseFeedFormats(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		body      string
		wantTitle string
		wantDate  string
		wantFiles int
	}{
		{
			name: "RSS 2.0",
			body: string([]byte{0xef, 0xbb, 0xbf}) + `<?xml version="1.0"?>
<rss version="2.0">
  <channel>
    <title>RSS Feed</title>
    <link>https://example.com/</link>
    <item>
      <guid>rss-1</guid>
      <title>RSS item</title>
      <link>https://example.com/rss</link>
      <pubDate>Mon, 15 Jan 2024 12:00:00 GMT</pubDate>
      <description>RSS summary</description>
      <enclosure url="" type="" length="10"/>
      <enclosure url="https://example.com/file.txt" type="text/plain" length="999999999999999999999999"/>
    </item>
  </channel>
</rss>`,
			wantTitle: "RSS item",
			wantDate:  "2024-01-15T12:00:00Z",
			wantFiles: 1,
		},
		{
			name: "Atom 1.0",
			body: `<?xml version="1.0"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Atom Feed</title>
  <link href="https://example.com/"/>
  <entry>
    <id>atom-1</id>
    <title>Atom item</title>
    <link href="https://example.com/atom"/>
    <updated>2024-01-16T12:00:00Z</updated>
    <summary>Atom summary</summary>
  </entry>
</feed>`,
			wantTitle: "Atom item",
			wantDate:  "2024-01-16T12:00:00Z",
		},
		{
			name: "RDF RSS 1.0",
			body: `<?xml version="1.0"?>
<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"
         xmlns="http://purl.org/rss/1.0/"
         xmlns:dc="http://purl.org/dc/elements/1.1/">
  <channel rdf:about="https://example.com/feed">
    <title>RDF Feed</title>
    <link>https://example.com/</link>
    <description>RDF description</description>
  </channel>
  <item rdf:about="https://example.com/rdf">
    <title>RDF item</title>
    <link>https://example.com/rdf</link>
    <dc:date>2024-01-17T12:00:00Z</dc:date>
  </item>
</rdf:RDF>`,
			wantTitle: "RDF item",
			wantDate:  "2024-01-17T12:00:00Z",
		},
		{
			name: "JSON Feed 1.1",
			body: string([]byte{0xef, 0xbb, 0xbf}) + `{
  "version": "https://jsonfeed.org/version/1.1",
  "title": "JSON Feed",
  "home_page_url": "https://example.com/",
  "feed_url": "https://canonical.example.com/feed",
  "items": [{
    "id": "json-1",
    "url": "https://example.com/json",
    "title": "JSON item",
    "content_text": "JSON content",
    "date_published": "2024-01-18T12:00:00+00:00",
    "attachments": [
      {"url": "", "mime_type": ""},
      {
        "url": "https://example.com/file.txt",
        "mime_type": "text/plain",
        "size_in_bytes": -1,
        "duration_in_seconds": -1
      }
    ]
  }]
}`,
			wantTitle: "JSON item",
			wantDate:  "2024-01-18T12:00:00Z",
			wantFiles: 1,
		},
		{
			name: "JSON Feed 1.0",
			body: `{
  "version": "https://jsonfeed.org/version/1",
  "title": "JSON Feed 1.0",
  "items": [{
    "id": "json-1.0",
    "url": "https://example.com/json-1.0",
    "title": "JSON 1.0 item",
    "content_text": "JSON content",
    "date_published": "2024-01-19T12:00:00Z"
  }]
}`,
			wantTitle: "JSON 1.0 item",
			wantDate:  "2024-01-19T12:00:00Z",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			items, err := parseFeed([]byte(tt.body), "https://example.com/feed")
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != 1 {
				t.Fatalf("got %d items, want 1", len(items))
			}
			if got := items[0].Title; got != tt.wantTitle {
				t.Errorf("title = %q, want %q", got, tt.wantTitle)
			}
			gotDate := firstNonEmpty(items[0].DatePublished, items[0].DateModified)
			if gotDate != tt.wantDate {
				t.Errorf("date = %q, want %q", gotDate, tt.wantDate)
			}
			if got := items[0].Feed.FeedURL; got != "https://example.com/feed" {
				t.Errorf("feed URL = %q", got)
			}
			if got := len(items[0].Attachments); got != tt.wantFiles {
				t.Errorf("attachments = %d, want %d", got, tt.wantFiles)
			}
			for _, attachment := range items[0].Attachments {
				if attachment.SizeInBytes < 0 || attachment.DurationInSeconds < 0 {
					t.Errorf("negative attachment values: %#v", attachment)
				}
			}
		})
	}
}

func TestParseFeedRejectsInvalidInput(t *testing.T) {
	t.Parallel()
	_, err := parseFeed([]byte("not a feed"), "https://example.com/feed")
	if err == nil || !strings.Contains(err.Error(), "parse feed") {
		t.Fatalf("error = %v", err)
	}
}

func TestParseFeedRejectsUnsupportedJSON(t *testing.T) {
	t.Parallel()
	body := []byte(`{"version":"https://jsonfeed.org/version/2","items":[]}`)
	_, err := parseFeed(body, "https://example.com/feed")
	if err == nil || !strings.Contains(err.Error(), "not a supported JSON Feed") {
		t.Fatalf("error = %v", err)
	}
}

func TestWithinPeriod(t *testing.T) {
	t.Parallel()
	since, err := parseTimeBound("2024-01-01", false)
	if err != nil {
		t.Fatal(err)
	}
	until, err := parseTimeBound("2024-01-31", true)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		date string
		want bool
	}{
		{"2023-12-31T23:59:59Z", false},
		{"2024-01-01T00:00:00Z", true},
		{"2024-01-31T23:59:59Z", true},
		{"2024-02-01T00:00:00Z", false},
		{"", false},
	}
	for _, tt := range tests {
		item := Item{DatePublished: tt.date}
		if got := withinPeriod(item, since, until); got != tt.want {
			t.Errorf("withinPeriod(%q) = %v, want %v", tt.date, got, tt.want)
		}
	}
}

func TestWithinPeriodFallsBackToModifiedDate(t *testing.T) {
	t.Parallel()
	since, err := parseTimeBound("2024-01-01", false)
	if err != nil {
		t.Fatal(err)
	}
	item := Item{
		DatePublished: "not-rfc3339",
		DateModified:  "2024-01-15T12:00:00Z",
	}
	if !withinPeriod(item, since, nil) {
		t.Error("parseable modification date should be used when publication date is invalid")
	}
}

func TestWithinPeriodAcceptsEitherPublishedOrModifiedDate(t *testing.T) {
	t.Parallel()
	since, err := parseTimeBound("2024-01-01", false)
	if err != nil {
		t.Fatal(err)
	}
	until, err := parseTimeBound("2024-01-31", true)
	if err != nil {
		t.Fatal(err)
	}
	item := Item{
		DatePublished: "2023-12-01T00:00:00Z",
		DateModified:  "2024-01-15T12:00:00Z",
	}
	if !withinPeriod(item, since, until) {
		t.Error("item should be included when its modification date is in range")
	}
}

func TestFractionalSecondDates(t *testing.T) {
	t.Parallel()
	const value = "2024-01-15T12:00:00.123456789Z"
	if got := normalizeDate(value); got != value {
		t.Errorf("normalizeDate(%q) = %q", value, got)
	}
	since, err := parseTimeBound("2024-01-15T12:00:00.123Z", false)
	if err != nil {
		t.Fatal(err)
	}
	if !withinPeriod(Item{DatePublished: value}, since, nil) {
		t.Error("fractional-second item should be included")
	}
}

func TestAtomContentAuthorsAndRelativeURLs(t *testing.T) {
	t.Parallel()
	body := []byte(`<feed xmlns="http://www.w3.org/2005/Atom">
	  <title>Feed</title>
	  <link href="/"/>
	  <author><name>Feed Author</name><uri>/authors/feed</uri></author>
	  <entry>
	    <id>entry-1</id>
	    <title>Entry</title>
	    <link href="posts/1"/>
	    <content type="text">&lt;em&gt;text&lt;/em&gt;</content>
	  </entry>
	</feed>`)
	items, err := parseFeed(body, "https://example.com/feeds/main.xml")
	if err != nil {
		t.Fatal(err)
	}
	if got := items[0].ContentText; got != "<em>text</em>" {
		t.Errorf("content_text = %q", got)
	}
	if got := items[0].ContentHTML; got != "" {
		t.Errorf("content_html = %q", got)
	}
	if got := items[0].URL; got != "https://example.com/feeds/posts/1" {
		t.Errorf("url = %q", got)
	}
	if got := items[0].Feed.HomePageURL; got != "https://example.com/" {
		t.Errorf("home page URL = %q", got)
	}
	if len(items[0].Authors) != 1 || items[0].Authors[0].Name != "Feed Author" ||
		items[0].Authors[0].URL != "https://example.com/authors/feed" {
		t.Errorf("authors = %#v", items[0].Authors)
	}
}

func TestRSSItemsWithoutStableIdentityAreRejected(t *testing.T) {
	t.Parallel()
	body := []byte(`<rss version="2.0"><channel><title>Feed</title>
	  <item><title>No identity</title><description>content</description></item>
	  <item><link>/stable</link><title>Stable</title><description>content</description></item>
	</channel></rss>`)
	items, err := parseFeed(body, "https://example.com/feed.xml")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "https://example.com/stable" {
		t.Fatalf("items = %#v", items)
	}
	if items[0].ContentHTML != "content" {
		t.Errorf("content_html = %q", items[0].ContentHTML)
	}
}

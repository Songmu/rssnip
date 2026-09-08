// Package rssnip tests in this file cover parsing feed bodies (RSS, Atom,
// RDF, and JSON Feed) into the normalized Item shape, including identity
// (ID) derivation and date validation.
package rssnip

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseFeedFormats(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		testdata  string
		wantTitle string
		wantDate  string
		wantFiles int
	}{
		{
			name:      "RSS 2.0",
			testdata:  "rss2.xml",
			wantTitle: "RSS item",
			wantDate:  "2024-01-15T12:00:00Z",
			wantFiles: 1,
		},
		{
			name:      "Atom 1.0",
			testdata:  "atom1.xml",
			wantTitle: "Atom item",
			wantDate:  "2024-01-16T12:00:00Z",
		},
		{
			name:      "RDF RSS 1.0",
			testdata:  "rdf1.xml",
			wantTitle: "RDF item",
			wantDate:  "2024-01-17T12:00:00Z",
		},
		{
			name:      "JSON Feed 1.1",
			testdata:  "jsonfeed1.1.json",
			wantTitle: "JSON item",
			wantDate:  "2024-01-18T12:00:00Z",
			wantFiles: 1,
		},
		{
			name:      "JSON Feed 1.0",
			testdata:  "jsonfeed1.0.json",
			wantTitle: "JSON 1.0 item",
			wantDate:  "2024-01-19T12:00:00Z",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			body := mustReadTestdata(t, tt.testdata)
			items, err := parseFeed(body, "https://example.com/feed")
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

func TestRSSItemsWithoutStableIdentityAreRejected(t *testing.T) {
	t.Parallel()
	body := mustReadTestdata(t, "rss_no_stable_identity.xml")
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

func TestJSONFeedGeneratedIDsUseFullItem(t *testing.T) {
	t.Parallel()
	original := jsonFeedItem{Title: "Title", Summary: "Summary"}
	tests := map[string]func(*jsonFeedItem){
		"content text":  func(item *jsonFeedItem) { item.ContentText = "text" },
		"content html":  func(item *jsonFeedItem) { item.ContentHTML = "<p>html</p>" },
		"external URL":  func(item *jsonFeedItem) { item.ExternalURL = "https://example.com/external" },
		"image":         func(item *jsonFeedItem) { item.Image = "https://example.com/image" },
		"banner image":  func(item *jsonFeedItem) { item.BannerImage = "https://example.com/banner" },
		"authors":       func(item *jsonFeedItem) { item.Authors = []Author{{Name: "Author"}} },
		"legacy author": func(item *jsonFeedItem) { item.Author = &Author{Name: "Author"} },
		"tags":          func(item *jsonFeedItem) { item.Tags = []string{"go"} },
		"attachments": func(item *jsonFeedItem) {
			item.Attachments = []jsonFeedAttachment{{URL: "https://example.com/file", MIMEType: "text/plain"}}
		},
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			modified := original
			change(&modified)
			body, err := json.Marshal(jsonFeed{
				Version: "https://jsonfeed.org/version/1.1",
				Items:   []jsonFeedItem{original, modified, modified},
			})
			if err != nil {
				t.Fatal(err)
			}
			items, err := parseFeed(body, "https://example.com/feed")
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != 3 {
				t.Fatalf("items = %#v", items)
			}
			if items[0].ID == items[1].ID {
				t.Errorf("distinct items have the same fallback ID: %s", items[0].ID)
			}
			if items[1].ID != items[2].ID || !strings.HasPrefix(items[1].ID, "urn:sha256:") {
				t.Errorf("fallback IDs are not deterministic: %#v", items)
			}
		})
	}
}

func TestJSONFeedKeepsExplicitIdentity(t *testing.T) {
	t.Parallel()
	body := []byte(`{"version":"https://jsonfeed.org/version/1.1","items":[
	  {"id":"stable","url":"https://example.com/post","content_text":"first"},
	  {"id":"stable","url":"https://example.com/post","content_text":"updated"},
	  {"url":"https://example.com/post","content_text":"first"},
	  {"url":"https://example.com/post","content_text":"updated"}
	]}`)
	items, err := parseFeed(body, "https://example.com/feed")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 4 {
		t.Fatalf("items = %#v", items)
	}
	for i, want := range []string{"stable", "stable", "https://example.com/post", "https://example.com/post"} {
		if items[i].ID != want {
			t.Errorf("items[%d].ID = %q, want %q", i, items[i].ID, want)
		}
	}
}

func TestJSONFeedOmitsInvalidDates(t *testing.T) {
	t.Parallel()
	body := mustReadTestdata(t, "jsonfeed_invalid_dates.json")
	items, err := parseFeed(body, "https://example.com/feed")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %#v", items)
	}
	data, err := json.Marshal(items[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"date_published"`) || strings.Contains(string(data), `"date_modified"`) {
		t.Errorf("invalid date fields should be omitted: %s", data)
	}
	if items[1].DatePublished != "" || items[1].DateModified != "2024-01-15T12:00:00.123Z" {
		t.Errorf("dates = %#v", items[1])
	}
	since := mustParseTimeBound(t, "2024-01-01", false)
	if withinPeriod(items[0], since, nil) || !withinPeriod(items[1], since, nil) {
		t.Error("date filtering must use remaining valid dates")
	}
}

func TestJSONFeedFractionalAttachmentDuration(t *testing.T) {
	t.Parallel()
	body := mustReadTestdata(t, "jsonfeed_fractional_duration.json")
	items, err := parseFeed(body, "https://example.com/feed")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || len(items[0].Attachments) != 2 {
		t.Fatalf("items = %#v", items)
	}
	if items[0].Attachments[0].DurationInSeconds != 1.5 || items[0].Attachments[1].DurationInSeconds != 0 {
		t.Errorf("attachments = %#v", items[0].Attachments)
	}
	var output strings.Builder
	if err := writeItems(&output, items, ".", false, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"duration_in_seconds":1.5`) {
		t.Errorf("fractional duration lost in jq output: %s", output.String())
	}
}

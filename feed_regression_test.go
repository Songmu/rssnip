package rssnip

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDocumentBaseIsAnAttribute(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		`<feed xmlns="http://www.w3.org/2005/Atom"></feed>`,
		`<feed xmlns="http://www.w3.org/2005/Atom"/>`,
		`<!-- <feed> --><a:feed xmlns:a="http://www.w3.org/2005/Atom" title="a > b"></a:feed>`,
	} {
		var root struct {
			Base string `xml:"http://www.w3.org/XML/1998/namespace base,attr"`
			Text string `xml:",chardata"`
		}
		const base = "https://example.com/feeds/?a=1&b=2"
		result := withDocumentBase([]byte(body), base)
		if err := xml.Unmarshal(result, &root); err != nil {
			t.Fatalf("invalid XML %q: %v", result, err)
		}
		if root.Base != base || root.Text != "" {
			t.Errorf("document base must be an attribute, got %#v in %s", root, result)
		}
	}
}

func TestAtomHTMLUsesDocumentBase(t *testing.T) {
	t.Parallel()
	body := []byte(`<feed xmlns="http://www.w3.org/2005/Atom">
	  <title>Feed</title>
	  <entry>
	    <id>entry</id>
	    <content type="html">&lt;a href="posts/one"&gt;article&lt;/a&gt;&lt;img src="images/one.png"/&gt;</content>
	  </entry>
	</feed>`)
	items, err := parseFeed(body, "https://example.com/feeds/main.xml")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %#v", items)
	}
	for _, want := range []string{
		`href="https://example.com/feeds/posts/one"`,
		`src="https://example.com/feeds/images/one.png"`,
	} {
		if !strings.Contains(items[0].ContentHTML, want) {
			t.Errorf("content_html = %q, want %q", items[0].ContentHTML, want)
		}
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

func TestJSONFeedOmitsInvalidDates(t *testing.T) {
	t.Parallel()
	body := []byte(`{"version":"https://jsonfeed.org/version/1.1","items":[
	  {"id":"invalid","date_published":"yesterday","date_modified":"2024-01-01"},
	  {"id":"modified","date_published":"invalid","date_modified":"2024-01-15T12:00:00.123Z"}
	]}`)
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
	since, err := parseTimeBound("2024-01-01", false)
	if err != nil {
		t.Fatal(err)
	}
	if withinPeriod(items[0], since, nil) || !withinPeriod(items[1], since, nil) {
		t.Error("date filtering must use remaining valid dates")
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

type errorTransport struct{ err error }

func (transport errorTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, transport.err
}

func TestFetchFeedPreservesNetworkErrors(t *testing.T) {
	t.Parallel()
	for _, cause := range []error{
		context.Canceled,
		context.DeadlineExceeded,
		&net.DNSError{Err: "no such host", Name: "example.invalid", IsNotFound: true},
		errors.New("TLS handshake failed"),
	} {
		client := &http.Client{Transport: errorTransport{err: cause}}
		_, err := fetchFeed(context.Background(), client, "https://example.invalid/feed")
		if !errors.Is(err, cause) {
			t.Errorf("error = %v, want wrapped %v", err, cause)
		}
		if err == nil || !strings.Contains(err.Error(), cause.Error()) {
			t.Errorf("error = %v, missing cause %v", err, cause)
		}
	}
}

func TestRunPreservesCanceledFetch(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	server := newFeedServer(t, testRSS)
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
		http.Redirect(w, r, "http://user:password@example.com/feed", http.StatusFound)
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

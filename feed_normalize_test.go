// This file covers Atom document normalization: xml:base resolution,
// content/media-type handling, and character encoding.
package rssnip

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestAtomNormalizationDoesNotInjectCharacterData(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
	}{
		{"empty element pair", `<feed xmlns="http://www.w3.org/2005/Atom"></feed>`},
		{"self-closing", `<feed xmlns="http://www.w3.org/2005/Atom"/>`},
		{
			"comment and prefixed namespace with attribute",
			`<!-- <feed> --><a:feed xmlns:a="http://www.w3.org/2005/Atom" title="a > b"></a:feed>`,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var root struct {
				Base string `xml:"http://www.w3.org/XML/1998/namespace base,attr"`
				Text string `xml:",chardata"`
			}
			const base = "https://example.com/feeds/?a=1&b=2"
			result, err := normalizeAtomDocument([]byte(tt.body), base)
			if err != nil {
				t.Fatal(err)
			}
			if err := xml.Unmarshal(result, &root); err != nil {
				t.Fatalf("invalid XML %q: %v", result, err)
			}
			if root.Base != "" || root.Text != "" {
				t.Errorf("consumed base must not remain as attribute or text, got %#v in %s", root, result)
			}
		})
	}
}

func TestAtomReferenceResolution(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, rootAttributes, entryAttributes, reference, want string
	}{
		{"query", "", "", "?page=2", "https://example.com/feeds/main.xml?page=2"},
		{"fragment", "", "", "#part", "https://example.com/feeds/main.xml?token=x#part"},
		{"relative", "", "", "article", "https://example.com/feeds/article"},
		{"file base", `xml:base="/posts/index.html?view=all"`, "", "#part", "https://example.com/posts/index.html?view=all#part"},
		{"spaced base", `xml:base = "/root/"`, "", "article", "https://example.com/root/article"},
		{"base in other attribute", `label="xml:base='/wrong/'"`, "", "article", "https://example.com/feeds/article"},
		{"escaped base", `xml:base="/root/?a=1&amp;b=2"`, "", "#part", "https://example.com/root/?a=1&b=2#part"},
		{"nested file base", `xml:base="/root/"`, `xml:base="entries/index.xml"`, "?page=2", "https://example.com/root/entries/index.xml?page=2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := `<feed xmlns="http://www.w3.org/2005/Atom" ` + tt.rootAttributes + `>
			  <entry ` + tt.entryAttributes + `><id>entry</id>
			    <link href="` + tt.reference + `"/>
			    <content type="html">&lt;a href="` + tt.reference + `"&gt;link&lt;/a&gt;</content>
			  </entry></feed>`
			items, err := parseFeed([]byte(body), "https://example.com/feeds/main.xml?token=x#old")
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != 1 {
				t.Fatalf("items = %#v", items)
			}
			if items[0].URL != tt.want {
				t.Errorf("URL = %q, want %q", items[0].URL, tt.want)
			}
			// HTML serialization escapes query separators.
			wantHTML := strings.ReplaceAll(tt.want, "&", "&amp;")
			if !strings.Contains(items[0].ContentHTML, `href="`+wantHTML+`"`) {
				t.Errorf("content = %q, want href %q", items[0].ContentHTML, tt.want)
			}
		})
	}
}

func TestAtomNormalizationPreservesContentAndBaseScopes(t *testing.T) {
	t.Parallel()
	body := mustReadTestdata(t, "atom_preserves_base_scopes.xml")
	items, err := parseFeed(body, "https://example.com/feed.xml")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %#v", items)
	}
	if items[0].URL != "https://example.com/posts/one.html#one" ||
		items[1].URL != "https://example.com/feed.xml#two" {
		t.Errorf("base scopes were not preserved: %#v", items)
	}
	for i, want := range []string{
		`href="https://example.com/posts/one.html?q=1"`,
		`src="https://example.com/feed.xml?image=2"`,
	} {
		if !strings.Contains(items[i].ContentHTML, want) {
			t.Errorf("items[%d].ContentHTML = %q, want %q", i, items[i].ContentHTML, want)
		}
		if len(items[i].Authors) != 1 || items[i].Authors[0].URL != "https://example.com/feed.xml?author=1" {
			t.Errorf("items[%d].Authors = %#v", i, items[i].Authors)
		}
	}
}

func TestAtomNormalizationHandlesSourceEncoding(t *testing.T) {
	t.Parallel()
	body := []byte("<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?>" +
		`<feed xmlns="http://www.w3.org/2005/Atom"><entry><id>one</id><title>Caf` +
		"\xe9" + `</title><content type="text">text</content></entry></feed>`)
	items, err := parseFeed(body, "https://example.com/feed")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Title != "Caf\u00e9" {
		t.Errorf("items = %#v", items)
	}
}

func TestAtomContentAuthorsAndRelativeURLs(t *testing.T) {
	t.Parallel()
	body := mustReadTestdata(t, "atom_content_authors.xml")
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

func TestAtomNestedXMLBaseAndHTMLMediaTypes(t *testing.T) {
	t.Parallel()
	body := mustReadTestdata(t, "atom_nested_base.xml")
	items, err := parseFeed(body, "https://example.com/feeds/main.xml")
	if err != nil {
		t.Fatal(err)
	}
	for i, wantURL := range []string{
		"https://example.com/root/entries/one",
		"https://example.com/root/entries/two",
	} {
		if got := items[i].URL; got != wantURL {
			t.Errorf("items[%d].URL = %q, want %q", i, got, wantURL)
		}
		if items[i].ContentHTML == "" || items[i].ContentText != "" {
			t.Errorf("items[%d] content = %#v", i, items[i])
		}
	}
}

func TestAtomHTMLUsesDocumentBase(t *testing.T) {
	t.Parallel()
	body := mustReadTestdata(t, "atom_html_document_base.xml")
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

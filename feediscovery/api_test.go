package feediscovery_test

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/Songmu/rssnip/feediscovery"
)

func TestFindAllMetadataAndOrder(t *testing.T) {
	t.Parallel()
	body := `<html><head>
	  <link rel="alternate feed" href="one.xml#rss" title="  Caf&#233; &amp; news  " type="APPLICATION/RSS+XML; charset=UTF-8">
	  <link rel="alternate" href="one.xml#atom" title="Other" type="application/atom+xml">
	  <link rel="feed" href="https://EXAMPLE.com:443/feeds/one.xml">
	  <link rel="feed" href="../updates?x=1&amp;y=2" title="">
	  <link rel="alternate" href="two.json">
	  <link rel="alternate" href="/feedback" title="Anatomy">
	  <link rel="feed" href="https://example.com/blog/index.html#feed">
	  <link rel="feed" href="ftp://example.com/rss">
	  <link rel="feed" href="https://:443/rss">
	  <link rel="feed" href="https://user:secret@example.com/rss">
	  <link rel="feed" href="%zz">
	  <link rel="feed" href="//[invalid">
	  <link rel="feed" href="">
	  <base href="/feeds/">
	</head></html>`
	want := []feediscovery.Link{
		{URL: "https://example.com/feeds/one.xml", Title: "  Caf\u00e9 & news  ", Type: "APPLICATION/RSS+XML; charset=UTF-8"},
		{URL: "https://example.com/feeds/one.xml", Title: "Other", Type: "application/atom+xml"},
		{URL: "https://example.com/feeds/one.xml"},
		{URL: "https://example.com/updates?x=1&y=2"},
		{URL: "https://example.com/feeds/two.json"},
	}
	for _, reader := range []io.Reader{
		strings.NewReader(body),
		bytes.NewReader([]byte(body)),
		iotest.OneByteReader(strings.NewReader(body)),
	} {
		got, err := feediscovery.FindAll(reader, "https://example.com/blog/index.html", "text/html; charset=utf-8")
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("links = %#v, want %#v", got, want)
		}
	}
}

func TestFindAllDecodedMetadata(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ prefix, contentType string }{
		{"", "text/html; charset=iso-8859-1"},
		{`<meta charset="iso-8859-1">`, ""},
		{`<?xml version="1.0" encoding="iso-8859-1"?>`, "application/xhtml+xml"},
	} {
		body := tt.prefix + `<html xmlns="http://www.w3.org/1999/xhtml"><head>` +
			`<link rel="feed" href="/rss" title="Caf` + "\xe9" + ` &amp; news" type=" application/rss+xml "/></head></html>`
		got, err := feediscovery.FindAll(strings.NewReader(body), "https://example.com/", tt.contentType)
		want := []feediscovery.Link{{
			URL: "https://example.com/rss", Title: "Caf\u00e9 & news", Type: " application/rss+xml ",
		}}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("content type %q: links = %#v, error = %v, want %#v", tt.contentType, got, err, want)
		}
	}
}

func TestFindAllNoMatches(t *testing.T) {
	t.Parallel()
	for _, body := range []string{"", "plain text", `{"items":[]}`, "<rss/>", "<html><title>Blog</title></html>"} {
		for _, contentType := range []string{"", "invalid header", "text/html", "application/xhtml+xml"} {
			got, err := feediscovery.FindAll(strings.NewReader(body), "https://example.com/", contentType)
			if err != nil || got != nil {
				t.Errorf("body %q, content type %q: links = %#v, error = %v", body, contentType, got, err)
			}
		}
	}
}

func TestFindAllPageURLContext(t *testing.T) {
	t.Parallel()
	for _, page := range []string{"https://example.com/blog/index.html", "http://other.example/articles/page#section"} {
		for _, base := range []string{"", `<base href="/feeds/">`} {
			body := `<html>` + base + `<link rel="feed" href="feed.xml"></html>`
			got, err := feediscovery.FindAll(strings.NewReader(body), page, "")
			if err != nil {
				t.Fatal(err)
			}
			host, path := "https://example.com", "/blog/"
			if strings.HasPrefix(page, "http:") {
				host, path = "http://other.example", "/articles/"
			}
			if base != "" {
				path = "/feeds/"
			}
			want := []feediscovery.Link{{URL: host + path + "feed.xml"}}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("page %q, base %q: got %#v, want %#v", page, base, got, want)
			}
		}
	}
}

func TestFindAllInvalidArguments(t *testing.T) {
	t.Parallel()
	for _, page := range []string{
		"", "/blog/", "//example.com/", "ftp://example.com/", "https:///blog",
		"https://:443/", "https://example.com:bad/", "https://[invalid",
		"https://user:secret@example.com/", "https://user:secret@%zz/",
	} {
		reader := &trackingReader{Reader: strings.NewReader("<html/>")}
		links, err := feediscovery.FindAll(reader, page, "")
		if err == nil || links != nil {
			t.Errorf("page %q: links = %#v, error = %v", page, links, err)
		}
		if err != nil && strings.Contains(err.Error(), "secret") {
			t.Errorf("page URL credentials leaked: %v", err)
		}
		if reader.read != 0 {
			t.Errorf("invalid page %q consumed %d bytes", page, reader.read)
		}
	}
	links, err := feediscovery.FindAll(nil, "https://example.com/", "")
	if err == nil || links != nil {
		t.Errorf("nil reader: links = %#v, error = %v", links, err)
	}
}

func TestFindAllReadFailuresAndOwnership(t *testing.T) {
	t.Parallel()
	body := []byte(`<html><link rel="feed" href="/rss"></html>`)
	original := bytes.Clone(body)
	readErr := errors.New("broken input")
	for _, terminalErr := range []error{readErr, io.EOF} {
		reader := &trackingReader{Reader: &terminalReader{body: body, err: terminalErr}}
		links, err := feediscovery.FindAll(reader, "https://example.com/", "")
		if terminalErr == io.EOF {
			if err != nil || len(links) != 1 {
				t.Errorf("data with EOF: links = %#v, error = %v", links, err)
			}
		} else if !errors.Is(err, readErr) || links != nil {
			t.Errorf("data with read failure: links = %#v, error = %v", links, err)
		}
		if reader.closed {
			t.Error("FindAll closed its reader")
		}
	}
	if !bytes.Equal(body, original) {
		t.Error("FindAll mutated the input")
	}
}

func TestFindAllSizeLimit(t *testing.T) {
	for _, size := range []int64{
		feediscovery.MaxDocumentSize,
		feediscovery.MaxDocumentSize + 1,
		feediscovery.MaxDocumentSize * 2,
	} {
		// Non-HTML input must still be checked for size before sniffing.
		reader := &trackingReader{Reader: io.MultiReader(
			strings.NewReader("<root/>"),
			io.LimitReader(spaceReader{}, size-int64(len("<root/>"))),
		)}
		links, err := feediscovery.FindAll(reader, "https://example.com/", "")
		if size > feediscovery.MaxDocumentSize {
			if !errors.Is(err, feediscovery.ErrTooLarge) {
				t.Errorf("size %d: error = %v, want ErrTooLarge", size, err)
			}
		} else if err != nil {
			t.Errorf("exact limit: %v", err)
		}
		if links != nil {
			t.Errorf("size %d: unexpected links %#v", size, links)
		}
		wantRead := min(size, feediscovery.MaxDocumentSize+1)
		if reader.read != wantRead {
			t.Errorf("size %d: read %d bytes, want %d", size, reader.read, wantRead)
		}
		if reader.closed {
			t.Error("FindAll closed its reader")
		}
	}
}

func TestFindAllRejectsPartialXHTMLResults(t *testing.T) {
	t.Parallel()
	root := `<html xmlns="http://www.w3.org/1999/xhtml">`
	link := `<link rel="feed" href="/rss"/>`
	base := `<base href="/feeds/"/>`
	for _, tt := range []struct{ name, body, stage string }{
		{"before base", root + `<bad></wrong>` + base + link + `</html>`, "scan document base"},
		{"without base", root + link + `<bad></wrong></html>`, "scan document base"},
		{"after base and link", root + base + link + `<bad></wrong></html>`, "scan feed links"},
		{"truncated", root + base + link + `<bad>`, "scan feed links"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			links, err := feediscovery.FindAll(strings.NewReader(tt.body), "https://example.com/", "application/xhtml+xml")
			if err == nil || !strings.Contains(err.Error(), tt.stage) || links != nil {
				t.Errorf("links = %#v, error = %v, want nil and %q error", links, err, tt.stage)
			}
		})
	}
	for _, tt := range []struct{ body, contentType string }{
		{root + link + "</html>", "application/xhtml+xml; charset=unknown-charset"},
		{`<?xml version="1.0" encoding="unknown-charset"?>` + root + link + "</html>", "application/xhtml+xml"},
	} {
		links, err := feediscovery.FindAll(strings.NewReader(tt.body), "https://example.com/", tt.contentType)
		if err == nil || !strings.Contains(err.Error(), "decode document") || links != nil {
			t.Errorf("links = %#v, error = %v, want nil and decode error", links, err)
		}
	}
}

type trackingReader struct {
	io.Reader
	read   int64
	closed bool
}

func (r *trackingReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.read += int64(n)
	return n, err
}

func (r *trackingReader) Close() error {
	r.closed = true
	return nil
}

type terminalReader struct {
	body []byte
	err  error
}

func (r *terminalReader) Read(p []byte) (int, error) {
	n := copy(p, r.body)
	r.body = r.body[n:]
	if len(r.body) == 0 {
		return n, r.err
	}
	return n, nil
}

type spaceReader struct{}

func (spaceReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = ' '
	}
	return len(p), nil
}

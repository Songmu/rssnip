package feediscovery

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestLooksLikeHTML(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name        string
		document    string
		contentType string
		want        bool
	}{
		{"content type", "not markup", "text/html; charset=utf-8", true},
		{"body element first", "<main><h1>Blog</h1></main>", "", true},
		{"metadata element first", "<link rel=alternate href=/feed>", "", true},
		{"RSS", "<rss><channel/></rss>", "", false},
		{"Atom", "<feed xmlns=\"http://www.w3.org/2005/Atom\"/>", "", false},
		{"SVG", "<svg xmlns=\"http://www.w3.org/2000/svg\"/>", "", false},
		{"plain text", "hello", "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := LooksLikeHTML([]byte(tt.document), tt.contentType); got != tt.want {
				t.Errorf("LooksLikeHTML() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestDocumentTokenizerErrors(t *testing.T) {
	t.Parallel()
	cause := errors.New("decode stream failure")
	for _, contentType := range []string{"text/html", "application/xhtml+xml"} {
		tokenizer := newDiscoveryTokenizer(errorReader{cause}, contentType)
		depth := 0
		if _, err := nextDocumentTag(tokenizer, &depth); !errors.Is(err, cause) {
			t.Errorf("%s: error = %v, want stream failure", contentType, err)
		}
		tokenizer = newDiscoveryTokenizer(strings.NewReader(""), contentType)
		if _, err := nextDocumentTag(tokenizer, &depth); err != io.EOF {
			t.Errorf("%s: error = %v, want EOF", contentType, err)
		}
	}
}

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }

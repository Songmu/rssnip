package feediscovery

import (
	"errors"
	"io"
	"strings"
	"testing"
)

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

package feediscovery_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/Songmu/rssnip/feediscovery"
)

func mustReadTestdata(t *testing.T, name string) []byte {
	t.Helper()
	// Preserve BOMs, legacy encodings, whitespace, and malformed document endings.
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func firstURL(t *testing.T, body []byte, pageURL, contentType string) (string, bool) {
	t.Helper()
	links, err := feediscovery.FindAll(bytes.NewReader(body), pageURL, contentType)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) == 0 {
		return "", false
	}
	return links[0].URL, true
}

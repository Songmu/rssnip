package rssnip

import (
	"bytes"
	"math"
	"strings"
	"testing"
)

func TestWriteItemsPreservesIntegersWithoutJQ(t *testing.T) {
	t.Parallel()
	item := Item{
		ID: "item",
		Attachments: []Attachment{{
			URL:         "https://example.com/file",
			MIMEType:    "application/octet-stream",
			SizeInBytes: math.MaxInt64,
		}},
		Feed: FeedInfo{FeedURL: "https://example.com/feed"},
	}
	var output bytes.Buffer
	if err := writeItems(&output, []Item{item}, "", false, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"size_in_bytes":9223372036854775807`) {
		t.Errorf("integer precision was not preserved: %s", output.String())
	}
}

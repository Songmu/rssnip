package rssnip

import (
	"bytes"
	"context"
	"errors"
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

func TestWriteItemsPreservesIntegersWithJQ(t *testing.T) {
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
	if err := writeItems(&output, []Item{item}, ".", false, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"size_in_bytes":9223372036854775807`) {
		t.Errorf("integer precision was not preserved through jq: %s", output.String())
	}
}

func TestWriteItemsWrapsRawOutputErrors(t *testing.T) {
	t.Parallel()
	item := Item{
		ID:    "item",
		Title: "title",
		Feed:  FeedInfo{FeedURL: "https://example.com/feed"},
	}
	err := writeItems(failingWriter{}, []Item{item}, ".title", true, false)
	if err == nil || !strings.Contains(err.Error(), "write output: write failed") {
		t.Fatalf("error = %v", err)
	}
}

func TestWriteItemValuesChecksContextWithoutJQ(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := writeItemValues(ctx, []Item{{ID: "item"}}, nil, func(any) error {
		t.Fatal("writeValue called")
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want %v", err, context.Canceled)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

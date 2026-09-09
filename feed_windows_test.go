//go:build windows

package rssnip

import (
	"net/url"
	"testing"
)

func TestLocalPathFromFileURLWindowsUNC(t *testing.T) {
	t.Parallel()
	parsed, err := url.Parse("file://server/share/feed.xml")
	if err != nil {
		t.Fatal(err)
	}
	got := localPathFromFileURL(parsed)
	want := `\\server\share\feed.xml`
	if got != want {
		t.Fatalf("localPathFromFileURL() = %q, want %q", got, want)
	}
}

func TestIsLocalFeedPathWindowsDriveRelative(t *testing.T) {
	t.Parallel()
	if !isLocalFeedPath(`C:feed.xml`) {
		t.Fatal("drive-relative path was not classified as local")
	}
}

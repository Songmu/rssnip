package rssnip

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// mustReadTestdata reads a fixture file from the testdata directory and
// fails the test immediately if it cannot be read.
func mustReadTestdata(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// mustParseTimeBound parses a UTC time bound and fails the test immediately on error.
func mustParseTimeBound(t *testing.T, value string) *time.Time {
	t.Helper()
	return mustParseTimeBoundInLocation(t, value, time.UTC)
}

func mustParseTimeBoundInLocation(t *testing.T, value string, location *time.Location) *time.Time {
	t.Helper()
	bound, err := parseTimeBound(value, location)
	if err != nil {
		t.Fatal(err)
	}
	return bound
}

// newFeedServer starts an httptest server that serves body for every
// request, asserting the User-Agent and Accept headers rssnip is expected
// to send.
func newFeedServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); !strings.HasPrefix(got, "rssnip/") {
			t.Errorf("User-Agent = %q", got)
		}
		accept := r.Header.Get("Accept")
		if !strings.Contains(accept, "application/json") || !strings.Contains(accept, "*/*") {
			t.Errorf("Accept = %q", accept)
		}
		w.Header().Set("Content-Type", "application/rss+xml")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(server.Close)
	return server
}

// newBadGatewayServer starts an httptest server that always responds with
// 502 Bad Gateway, used to exercise upstream-failure handling.
func newBadGatewayServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no", http.StatusBadGateway)
	}))
	t.Cleanup(server.Close)
	return server
}

// This file covers network-level behavior of feed fetching: error
// propagation, cancellation, and redirect handling.
package rssnip

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type errorTransport struct{ err error }

func (transport errorTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, transport.err
}

func TestFetchFeedPreservesNetworkErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		cause error
	}{
		{"canceled", context.Canceled},
		{"deadline exceeded", context.DeadlineExceeded},
		{"dns error", &net.DNSError{Err: "no such host", Name: "example.invalid", IsNotFound: true}},
		{"generic error", errors.New("TLS handshake failed")},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			client := &http.Client{Transport: errorTransport{err: tt.cause}}
			_, err := fetchFeed(context.Background(), client, "https://example.invalid/feed")
			if !errors.Is(err, tt.cause) {
				t.Errorf("error = %v, want wrapped %v", err, tt.cause)
			}
			if err == nil || !strings.Contains(err.Error(), tt.cause.Error()) {
				t.Errorf("error = %v, missing cause %v", err, tt.cause)
			}
		})
	}
}

func TestRunPreservesCanceledFetch(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	server := newFeedServer(t, string(mustReadTestdata(t, "sample_rss.xml")))
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
		http.Redirect(w, r, "******example.com/feed", http.StatusFound)
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

package feediscovery

import "testing"

func TestDiscoveryFetchURL(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ input, want string }{
		{"HTTP://[::1]:80/blog#rss", "http://[::1]/blog"},
		{"http://[::1]/blog", "http://[::1]/blog"},
		{"https://EXAMPLE.com:443#rss", "https://example.com/"},
		{"https://example.com:8443/blog?q=1#rss", "https://example.com:8443/blog?q=1"},
		{"http://example.com/blog%2Ffeed?q=%23#rss", "http://example.com/blog%2Ffeed?q=%23"},
	} {
		if got := discoveryFetchURL(tt.input); got != tt.want {
			t.Errorf("normalized %q = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestDiscoverableFeedURL(t *testing.T) {
	t.Parallel()
	for _, scheme := range []string{"HTTP", "HTTPS", "hTtPs"} {
		if !isDiscoverableFeedURL(scheme + "://example.com/feed.xml") {
			t.Errorf("valid scheme rejected: %q", scheme)
		}
	}
}

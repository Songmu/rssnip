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
		{"HTTP://[FE80::AB%25ETH0]:80/blog#rss", "http://[fe80::ab%25ETH0]/blog"},
		{"https://[FE80::AB%25EtherNet]:8443/rss", "https://[fe80::ab%25EtherNet]:8443/rss"},
		{"https://[FE80::AB%25Zone%20A]:443", "https://[fe80::ab%25Zone%20A]/"},
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

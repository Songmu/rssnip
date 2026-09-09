package feediscovery

import (
	"net/url"
	"strings"
)

func resolveCandidateURL(base *url.URL, href string) (string, bool) {
	reference, err := url.Parse(href)
	if err != nil {
		return "", false
	}
	resolved := discoveryFetchURL(base.ResolveReference(reference).String())
	return resolved, isDiscoverableFeedURL(resolved)
}

func discoveryFetchURL(value string) string {
	parsed, err := url.Parse(value)
	if err != nil {
		return value
	}
	parsed.Fragment, parsed.RawFragment = "", ""
	if zone := strings.IndexByte(parsed.Host, '%'); strings.HasPrefix(parsed.Host, "[") && zone >= 0 {
		// IPv6 zone identifiers are not DNS names; preserve their spelling.
		parsed.Host = strings.ToLower(parsed.Host[:zone]) + parsed.Host[zone:]
	} else {
		parsed.Host = strings.ToLower(parsed.Host)
	}
	if (parsed.Scheme == "http" && parsed.Port() == "80") ||
		(parsed.Scheme == "https" && parsed.Port() == "443") {
		host := parsed.Hostname()
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
		parsed.Host = host
	}
	// HTTP requests for an empty path target "/", too.
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	return parsed.String()
}

func isDiscoverableFeedURL(value string) bool {
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Hostname() == "" {
		return false
	}
	if parsed.User != nil {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

// Package rssnip fetches and parses RSS, Atom, RDF, and JSON Feed documents
// and normalizes their entries to the JSON Feed 1.1 item shape.
//
// Fetch retrieves a single feed URL over HTTP and returns its items. Parse does
// the same for a document the caller already holds, without any network access.
// Both accept the same Option values, and both return items in feed order with
// dates formatted as RFC3339. Run implements the rssnip command and is not part
// of the library API for programmatic use.
//
// Fetch accepts a feed URL or an HTML page advertising one. Discovery inspects
// only the first retrieved document, uses the first advertised link, and can be
// turned off with WithDiscovery. Pagination follows Atom rel="next" links, JSON
// Feed next_url, and WordPress paged feeds, stops at WithMaxPages feed pages or
// at a repeated URL, and drops items whose ID was already seen. Discovery may
// retrieve one additional HTML document before the feed pages. With WithSince,
// pagination may also stop early once a feed is observed to be ordered by date
// and to have run past the requested boundary.
//
// WithSince and WithUntil select the half-open interval [since, until) using
// each item's published date, or its updated date under WithPreferUpdated. The
// other date is used when the preferred one is missing or unparseable; items
// with no usable date are dropped whenever either bound is set.
//
// Each document is limited to MaxFeedSize bytes, and requests made by the
// default client are serialized per host, spaced one second apart, and time out
// after 30 seconds. That pacing state is created per Fetch call; pass
// WithHTTPClient to control it. Parse never closes the reader it is given and
// never fetches anything, so cancellation and timeouts belong to the caller.
//
// Unparseable documents match ErrNotFeed, documents that advertise no feed link
// match ErrNoFeedFound, and non-2xx responses return a *StatusError, except that
// a 404 or 410 response to a guessed WordPress pagination page ends pagination
// and keeps the items collected so far. Errors carry no partial items and report
// URLs with any userinfo removed. Use
// [github.com/Songmu/rssnip/feediscovery] to list the feed links an HTML page
// advertises without fetching them.
package rssnip

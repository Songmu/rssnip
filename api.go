package rssnip

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Songmu/rssnip/feediscovery"
)

// ErrNotFeed indicates that a document could not be parsed as a supported feed.
var ErrNotFeed = errors.New("rssnip: document is not a supported feed")

// ErrNoFeedFound indicates that a document is not a feed and advertises no feed
// link that could be followed.
var ErrNoFeedFound = errors.New("rssnip: no feed found for document")

// notFeedError attaches sentinels to a cause while preserving its message.
type notFeedError struct {
	cause     error
	sentinels []error
}

func (err *notFeedError) Error() string { return err.cause.Error() }

func (err *notFeedError) Unwrap() []error {
	return append([]error{err.cause}, err.sentinels...)
}

func notFeed(cause error) error {
	return &notFeedError{cause: cause, sentinels: []error{ErrNotFeed}}
}

func noFeedFound(cause error) error {
	return &notFeedError{cause: cause, sentinels: []error{ErrNotFeed, ErrNoFeedFound}}
}

// Option configures Fetch and Parse. Invalid values are reported by Fetch and
// Parse rather than when the option is created.
type Option func(*options) error

type options struct {
	client        *http.Client
	userAgent     string
	maxPages      int
	since         *time.Time
	until         *time.Time
	preferUpdated bool
	discovery     bool
}

func defaultOptions() *options {
	return &options{
		userAgent: defaultUserAgent,
		maxPages:  defaultMaxPages,
		discovery: true,
	}
}

func newOptions(opts []Option) (*options, error) {
	resolved := defaultOptions()
	for _, opt := range opts {
		if opt == nil {
			return nil, errors.New("rssnip: option must not be nil")
		}
		if err := opt(resolved); err != nil {
			return nil, err
		}
	}
	if resolved.since != nil && resolved.until != nil && resolved.since.After(*resolved.until) {
		return nil, errors.New("rssnip: since must not be after until")
	}
	if resolved.client == nil {
		resolved.client = &http.Client{Transport: newPoliteTransport(nil)}
	}
	return resolved, nil
}

// WithHTTPClient sets the HTTP client used by Fetch.
//
// By default Fetch creates a client whose transport serializes requests per
// host, waits one second between them, and applies a 30 second timeout to each
// request. That state is not shared between calls. A supplied client replaces
// this behavior, so polite pacing, timeouts, and redirect policy become the
// caller's responsibility.
func WithHTTPClient(client *http.Client) Option {
	return func(o *options) error {
		if client == nil {
			return errors.New("rssnip: HTTP client must not be nil")
		}
		o.client = client
		return nil
	}
}

// WithUserAgent sets the User-Agent header sent by Fetch. It defaults to
// "rssnip/<version>" and must not be empty.
func WithUserAgent(userAgent string) Option {
	return func(o *options) error {
		if strings.TrimSpace(userAgent) == "" {
			return errors.New("rssnip: user agent must not be empty")
		}
		o.userAgent = userAgent
		return nil
	}
}

// WithMaxPages limits how many feed pages Fetch retrieves. It defaults to 10
// and must be at least 1; 1 disables pagination. When discovery is enabled,
// Fetch may retrieve an additional HTML document before the feed pages. Parse
// ignores it because Parse never follows pagination links.
func WithMaxPages(maxPages int) Option {
	return func(o *options) error {
		if maxPages < 1 {
			return errors.New("rssnip: max pages must be at least 1")
		}
		o.maxPages = maxPages
		return nil
	}
}

// WithSince keeps only items dated at or after since. Fetch also uses it to
// stop paginating once a feed is observed to run past the boundary.
func WithSince(since time.Time) Option {
	return func(o *options) error {
		value := since
		o.since = &value
		return nil
	}
}

// WithUntil keeps only items dated strictly before until. Items are selected
// from the half-open interval [since, until).
func WithUntil(until time.Time) Option {
	return func(o *options) error {
		value := until
		o.until = &value
		return nil
	}
}

// WithPreferUpdated compares the updated date instead of the published date
// when filtering by WithSince or WithUntil. The other date is used when the
// preferred one is missing or unparseable.
func WithPreferUpdated(preferUpdated bool) Option {
	return func(o *options) error {
		o.preferUpdated = preferUpdated
		return nil
	}
}

// WithDiscovery enables or disables feed discovery for the first document that
// Fetch retrieves. It is enabled by default. Parse never discovers feeds.
func WithDiscovery(discovery bool) Option {
	return func(o *options) error {
		o.discovery = discovery
		return nil
	}
}

// Fetch retrieves feedURL and returns its items in feed order, normalized to
// the JSON Feed 1.1 item shape.
//
// feedURL must be an absolute HTTP or HTTPS URL without userinfo. It may point
// at a feed or, unless WithDiscovery(false) is set, at an HTML page advertising
// one; the first advertised link is then fetched instead. Fetch follows Atom
// rel="next" links, JSON Feed next_url, and WordPress paged feeds up to
// WithMaxPages feed pages, skipping items whose ID was already seen. When
// discovery is enabled, Fetch may retrieve an additional HTML document. Each
// document is limited to MaxFeedSize bytes.
//
// A non-2xx response returns a *StatusError, except that a 404 or 410 response
// to a guessed WordPress pagination page ends pagination and returns the items
// collected so far. A document that is neither a feed
// nor a page advertising one matches ErrNoFeedFound, and every unparseable
// document matches ErrNotFeed. Errors never carry partial items, and URLs in
// error messages have any userinfo removed.
func Fetch(ctx context.Context, feedURL string, opts ...Option) ([]Item, error) {
	if ctx == nil {
		return nil, errors.New("rssnip: context must not be nil")
	}
	resolved, err := newOptions(opts)
	if err != nil {
		return nil, err
	}
	items, err := fetchFeedPagesWithOptions(ctx, feedURL, resolved)
	if err != nil {
		return nil, err
	}
	return filterItems(items, resolved), nil
}

// Parse reads a single feed document from r and returns its items, normalized
// to the JSON Feed 1.1 item shape.
//
// Parse performs no network access and follows no pagination links, so
// WithHTTPClient, WithUserAgent, WithMaxPages, and WithDiscovery have no
// effect. sourceURL must be an absolute HTTP or HTTPS URL without userinfo; it
// resolves relative URLs in the document and becomes each item's _feed.feed_url.
// contentType is the document's HTTP Content-Type header and may be empty when
// unknown; RSS, Atom, RDF, and JSON Feed documents are recognized from their
// contents, and the character encoding of XML feeds comes from their byte order
// mark or XML declaration.
//
// Parse reads at most MaxFeedSize+1 bytes from r and fails without partial
// items when the document is larger. The caller owns r, including closing it.
// An HTML page is not a feed and matches ErrNotFeed; use feediscovery.FindAll
// to obtain the feed links it advertises.
func Parse(r io.Reader, sourceURL, contentType string, opts ...Option) ([]Item, error) {
	if r == nil {
		return nil, errors.New("rssnip: document reader is nil")
	}
	resolved, err := newOptions(opts)
	if err != nil {
		return nil, err
	}
	if err := validateDocumentURL(sourceURL); err != nil {
		return nil, err
	}
	body, err := readFeedDocument(r, sourceURL)
	if err != nil {
		return nil, err
	}
	items, _, err := parseFeedDetails(body, displayURL(sourceURL))
	if err != nil {
		if feediscovery.LooksLikeHTML(body, contentType) {
			return nil, notFeed(fmt.Errorf(
				"%w; the document is an HTML page: use feediscovery.FindAll to list its feed links", err))
		}
		return nil, notFeed(err)
	}
	return filterItems(items, resolved), nil
}

func filterItems(items []Item, opts *options) []Item {
	if opts.since == nil && opts.until == nil {
		return items
	}
	filtered := items[:0]
	for _, item := range items {
		if withinPeriod(item, opts.since, opts.until, opts.preferUpdated) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func validateDocumentURL(documentURL string) error {
	parsed, err := url.ParseRequestURI(documentURL)
	if err != nil || parsed.Host == "" || parsed.Hostname() == "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("rssnip: source URL %q must be an absolute HTTP or HTTPS URL",
			displayURL(documentURL))
	}
	if parsed.User != nil {
		return fmt.Errorf("rssnip: source URL %q must not contain userinfo", displayURL(documentURL))
	}
	return nil
}

func readFeedDocument(r io.Reader, sourceURL string) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, maxFeedSize+1))
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", displayURL(sourceURL), err)
	}
	if len(body) > maxFeedSize {
		return nil, fmt.Errorf("read %q: feed exceeds %d MiB limit",
			displayURL(sourceURL), maxFeedSize>>20)
	}
	return body, nil
}

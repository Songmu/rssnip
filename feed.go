package rssnip

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Songmu/rssnip/feediscovery"
	"github.com/mmcdole/gofeed"
	"github.com/mmcdole/gofeed/atom"
	"golang.org/x/net/html/charset"
)

const defaultUserAgent = "rssnip/" + version

// MaxFeedSize is the maximum size in bytes of a single feed document.
const MaxFeedSize = 32 << 20

const maxFeedSize = MaxFeedSize
const defaultMaxPages = 10
const minDateOrderSamples = 5

// StatusError reports a feed response with a non-2xx HTTP status.
type StatusError struct {
	// URL is the requested feed URL with any userinfo removed.
	URL string
	// Status is the HTTP status line, such as "404 Not Found".
	Status string
	// StatusCode is the HTTP status code, such as 404.
	StatusCode int
}

func (err *StatusError) Error() string {
	return fmt.Sprintf("fetch %q: unexpected HTTP status %s", err.URL, err.Status)
}

type paginationMode int

const (
	paginationNone paginationMode = iota
	paginationExplicit
	paginationWordPress
)

type dateOrderCandidate struct {
	previous time.Time
	count    int
	rejected bool
}

func (candidate *dateOrderCandidate) observe(itemTime time.Time, ok bool) {
	if !ok {
		return
	}
	if candidate.count > 0 && itemTime.After(candidate.previous) {
		candidate.rejected = true
	}
	candidate.previous = itemTime
	candidate.count++
}

func (candidate *dateOrderCandidate) exhausted(since time.Time) bool {
	return !candidate.rejected &&
		candidate.count >= minDateOrderSamples &&
		candidate.previous.Before(since)
}

type paginationDateOrder struct {
	published               dateOrderCandidate
	modified                dateOrderCandidate
	modifiedBoundsPublished bool
}

func newPaginationDateOrder() paginationDateOrder {
	return paginationDateOrder{modifiedBoundsPublished: true}
}

func (order *paginationDateOrder) observe(item Item) {
	published, publishedOK := filterDate(item, false)
	modified, modifiedOK := filterDate(item, true)
	order.published.observe(published, publishedOK)
	order.modified.observe(modified, modifiedOK)

	directPublished, directPublishedOK := parseItemDate(item.DatePublished)
	directModified, directModifiedOK := parseItemDate(item.DateModified)
	if directPublishedOK && directModifiedOK && directPublished.After(directModified) {
		order.modifiedBoundsPublished = false
	}
}

func (order *paginationDateOrder) exhausted(since time.Time, preferUpdated bool) bool {
	if preferUpdated {
		return order.modified.exhausted(since)
	}
	return order.published.exhausted(since) ||
		(order.modifiedBoundsPublished && order.modified.exhausted(since))
}

// Item is a feed entry normalized to the JSON Feed 1.1 item shape.
type Item struct {
	ID            string       `json:"id"`
	URL           string       `json:"url,omitempty"`
	ExternalURL   string       `json:"external_url,omitempty"`
	Title         string       `json:"title,omitempty"`
	ContentHTML   string       `json:"content_html,omitempty"`
	ContentText   string       `json:"content_text,omitempty"`
	Summary       string       `json:"summary,omitempty"`
	Image         string       `json:"image,omitempty"`
	BannerImage   string       `json:"banner_image,omitempty"`
	DatePublished string       `json:"date_published,omitempty"`
	DateModified  string       `json:"date_modified,omitempty"`
	Authors       []Author     `json:"authors,omitempty"`
	Tags          []string     `json:"tags,omitempty"`
	Attachments   []Attachment `json:"attachments,omitempty"`
	Feed          FeedInfo     `json:"_feed"`
}

type Author struct {
	Name   string `json:"name,omitempty"`
	URL    string `json:"url,omitempty"`
	Avatar string `json:"avatar,omitempty"`
}

type Attachment struct {
	URL               string  `json:"url"`
	MIMEType          string  `json:"mime_type"`
	Title             string  `json:"title,omitempty"`
	SizeInBytes       int64   `json:"size_in_bytes,omitempty"`
	DurationInSeconds float64 `json:"duration_in_seconds,omitempty"`
}

type FeedInfo struct {
	Title       string `json:"title,omitempty"`
	HomePageURL string `json:"home_page_url,omitempty"`
	FeedURL     string `json:"feed_url"`
}

type jsonFeed struct {
	Version     string         `json:"version"`
	Title       string         `json:"title"`
	HomePageURL string         `json:"home_page_url"`
	Authors     []Author       `json:"authors"`
	Author      *Author        `json:"author"`
	Items       []jsonFeedItem `json:"items"`
	NextURL     string         `json:"next_url"`
}

type jsonFeedItem struct {
	ID            string               `json:"id"`
	URL           string               `json:"url"`
	ExternalURL   string               `json:"external_url"`
	Title         string               `json:"title"`
	ContentHTML   string               `json:"content_html"`
	ContentText   string               `json:"content_text"`
	Summary       string               `json:"summary"`
	Image         string               `json:"image"`
	BannerImage   string               `json:"banner_image"`
	DatePublished string               `json:"date_published"`
	DateModified  string               `json:"date_modified"`
	Authors       []Author             `json:"authors"`
	Author        *Author              `json:"author"`
	Tags          []string             `json:"tags"`
	Attachments   []jsonFeedAttachment `json:"attachments"`
}

type jsonFeedAttachment struct {
	URL               string  `json:"url"`
	MIMEType          string  `json:"mime_type"`
	Title             string  `json:"title"`
	SizeInBytes       int64   `json:"size_in_bytes"`
	DurationInSeconds float64 `json:"duration_in_seconds"`
}

func fetchFeed(ctx context.Context, client *http.Client, feedURL string) ([]Item, error) {
	return fetchFeedPages(ctx, client, feedURL, defaultMaxPages)
}

func fetchFeedPages(ctx context.Context, client *http.Client, feedURL string, maxPages int) ([]Item, error) {
	return fetchFeedPagesSince(ctx, client, feedURL, maxPages, nil, false)
}

func fetchFeedPagesSince(
	ctx context.Context,
	client *http.Client,
	feedURL string,
	maxPages int,
	since *time.Time,
	preferUpdated bool,
) ([]Item, error) {
	opts := defaultOptions()
	opts.client = client
	opts.maxPages = maxPages
	opts.since = since
	opts.preferUpdated = preferUpdated
	return fetchFeedPagesWithOptions(ctx, feedURL, opts)
}

func fetchFeedPagesWithOptions(ctx context.Context, feedURL string, opts *options) ([]Item, error) {
	maxPages, since, preferUpdated := opts.maxPages, opts.since, opts.preferUpdated
	items := make([]Item, 0)
	seenItems := make(map[string]struct{})
	seenURLs := make(map[string]struct{})
	nextURL := feedURL
	mode := paginationNone
	wordPressPage := 0
	order := newPaginationDateOrder()
	for page := 0; page < maxPages && nextURL != ""; page++ {
		nextURL = paginationURL(nextURL)
		if _, ok := seenURLs[nextURL]; ok {
			break
		}
		seenURLs[nextURL] = struct{}{}

		pageItems, sourceURL, followingURL, wordPress, err := fetchFeedPage(
			ctx, opts, nextURL, page == 0 && opts.discovery)
		if err != nil {
			if mode == paginationWordPress && isMissingWordPressPage(err) {
				break
			}
			return nil, err
		}
		seenURLs[paginationURL(sourceURL)] = struct{}{}
		newItems := 0
		for _, item := range pageItems {
			if since != nil {
				order.observe(item)
			}
			if _, ok := seenItems[item.ID]; ok {
				continue
			}
			seenItems[item.ID] = struct{}{}
			items = append(items, item)
			newItems++
		}
		switch mode {
		case paginationExplicit:
			nextURL = followingURL
		case paginationWordPress:
			// The initial feed selects the pagination mode. Ignore later rel=next
			// links rather than switching schemes in the middle of the sequence.
			if newItems == 0 {
				nextURL = ""
			} else {
				nextURL, wordPressPage = nextWordPressPageURL(sourceURL, wordPressPage)
			}
		default:
			switch {
			case followingURL != "":
				mode = paginationExplicit
				nextURL = followingURL
			case wordPress && len(pageItems) > 0:
				mode = paginationWordPress
				wordPressPage = currentWordPressPage(sourceURL)
				nextURL, wordPressPage = nextWordPressPageURL(sourceURL, wordPressPage)
			default:
				nextURL = ""
			}
		}
		// This intentionally trusts the fetched prefix. An unseen later page may
		// violate the observed order; early termination accepts that tradeoff.
		if since != nil && order.exhausted(*since, preferUpdated) {
			break
		}
	}
	return items, nil
}

func fetchFeedPage(ctx context.Context, opts *options, feedURL string, allowDiscovery bool) ([]Item, string, string, bool, error) {
	body, sourceURL, contentType, err := fetchFeedDocument(ctx, opts, feedURL)
	if err != nil {
		return nil, "", "", false, err
	}
	items, generator, parseErr := parseFeedDetails(body, sourceURL)
	if parseErr != nil {
		if !allowDiscovery {
			return nil, "", "", false, notFeed(parseErr)
		}
		links, discoveryErr := feediscovery.FindAll(bytes.NewReader(body), sourceURL, contentType)
		if discoveryErr != nil {
			return nil, "", "", false, notFeed(
				fmt.Errorf("%w; discover feed links: %w", parseErr, discoveryErr))
		}
		if len(links) == 0 {
			return nil, "", "", false, noFeedFound(parseErr)
		}
		body, sourceURL, _, err = fetchFeedDocument(ctx, opts, links[0].URL)
		if err != nil {
			return nil, "", "", false, err
		}
		items, generator, err = parseFeedDetails(body, sourceURL)
		if err != nil {
			return nil, "", "", false, notFeed(err)
		}
	}
	nextURL, _ := nextPageURL(body, sourceURL)
	return items, sourceURL, nextURL, isWordPressGenerator(generator), nil
}

func fetchFeedDocument(ctx context.Context, opts *options, feedURL string) ([]byte, string, string, error) {
	parsedURL, err := url.ParseRequestURI(feedURL)
	if err != nil || parsedURL.Host == "" || parsedURL.Hostname() == "" ||
		(parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		if err == nil {
			err = fmt.Errorf("must be an absolute HTTP or HTTPS URL")
		}
		return nil, "", "", fmt.Errorf("invalid feed URL %q: %w", displayURL(feedURL), err)
	}
	if parsedURL.User != nil {
		return nil, "", "", fmt.Errorf("invalid feed URL %q: userinfo is not allowed", displayURL(feedURL))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feedURL, nil)
	if err != nil {
		return nil, "", "", fmt.Errorf("create request for %q: %w", displayURL(feedURL), err)
	}
	req.Header.Set("User-Agent", opts.userAgent)
	req.Header.Set("Accept", "application/feed+json, application/json, application/atom+xml, application/rss+xml, application/rdf+xml, application/xml, text/xml, */*;q=0.1")

	resp, err := doRequest(opts.client, req)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			redacted := *urlErr
			redacted.URL = displayURL(redacted.URL)
			err = &redacted
		}
		return nil, "", "", fmt.Errorf("fetch %q: %w", displayURL(feedURL), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return nil, "", "", &StatusError{
			URL:        displayURL(feedURL),
			Status:     resp.Status,
			StatusCode: resp.StatusCode,
		}
	}
	if resp.ContentLength > maxFeedSize {
		return nil, "", "", fmt.Errorf("read %q: feed exceeds %d MiB limit", displayURL(feedURL), maxFeedSize>>20)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFeedSize+1))
	if err != nil {
		return nil, "", "", fmt.Errorf("read %q: %w", displayURL(feedURL), err)
	}
	if len(body) > maxFeedSize {
		return nil, "", "", fmt.Errorf("read %q: feed exceeds %d MiB limit", displayURL(feedURL), maxFeedSize>>20)
	}
	sourceURL := feedURL
	if resp.Request != nil && resp.Request.URL != nil {
		sourceURL = resp.Request.URL.String()
	}
	sourceURL = displayURL(sourceURL)
	return body, sourceURL, resp.Header.Get("Content-Type"), nil
}

func parseFeed(body []byte, sourceURL string) ([]Item, error) {
	items, _, err := parseFeedDetails(body, sourceURL)
	return items, err
}

func parseFeedDetails(body []byte, sourceURL string) ([]Item, string, error) {
	body = bytes.TrimPrefix(body, []byte{0xef, 0xbb, 0xbf})
	if looksLikeJSON(body) {
		items, recognized, err := parseJSONFeed(body, sourceURL)
		if err != nil {
			return nil, "", err
		}
		if !recognized {
			return nil, "", fmt.Errorf("parse feed %q: JSON document is not a supported JSON Feed", sourceURL)
		}
		return items, "", nil
	}

	parser := gofeed.NewParser()
	parser.KeepOriginalFeed = true
	normalized, err := normalizeAtomDocument(body, sourceURL)
	if err != nil {
		return nil, "", fmt.Errorf("parse feed %q: %w", sourceURL, err)
	}
	feed, err := parser.Parse(bytes.NewReader(normalized))
	if err != nil {
		return nil, "", fmt.Errorf("parse feed %q: %w", sourceURL, err)
	}
	info := FeedInfo{
		Title:       feed.Title,
		HomePageURL: resolveURL(sourceURL, feed.Link),
		FeedURL:     sourceURL,
	}
	var atomFeed *atom.Feed
	if parsed, ok := feed.OriginalFeed().(*atom.Feed); ok {
		atomFeed = parsed
	}
	items := make([]Item, 0, len(feed.Items))
	for i, source := range feed.Items {
		if source.GUID == "" && source.Link == "" {
			continue
		}
		contentHTML, contentText := source.Content, ""
		if atomFeed != nil && i < len(atomFeed.Entries) {
			contentHTML, contentText = atomContent(atomFeed.Entries[i])
		} else if contentHTML == "" {
			contentHTML = source.Description
		}
		var authors []Author
		if atomFeed != nil && i < len(atomFeed.Entries) && len(atomFeed.Entries[i].Authors) > 0 {
			authors = atomAuthors(sourceURL, atomFeed.Entries[i].Authors)
		} else {
			for _, person := range source.Authors {
				authors = append(authors, Author{Name: person.Name})
			}
		}
		if len(authors) == 0 {
			if atomFeed != nil {
				authors = atomAuthors(sourceURL, atomFeed.Authors)
			} else {
				for _, person := range feed.Authors {
					authors = append(authors, Author{Name: person.Name})
				}
			}
		}
		id := source.GUID
		if id == "" {
			id = resolveURL(sourceURL, source.Link)
		}
		item := Item{
			ID:          id,
			URL:         resolveURL(sourceURL, source.Link),
			Title:       source.Title,
			ContentHTML: contentHTML,
			ContentText: contentText,
			Summary:     source.Description,
			Image:       resolveURL(sourceURL, imageURL(source.Image)),
			Tags:        append([]string(nil), source.Categories...),
			Feed:        info,
		}
		if item.ContentHTML == "" && item.ContentText == "" {
			if atomFeed != nil {
				item.ContentText = source.Description
			} else {
				item.ContentHTML = source.Description
			}
		}
		item.DatePublished = formatTime(source.PublishedParsed)
		item.DateModified = formatTime(source.UpdatedParsed)
		item.Authors = authors
		for _, enclosure := range source.Enclosures {
			var size int64
			if parsed, err := strconv.ParseInt(enclosure.Length, 10, 64); err == nil && parsed > 0 {
				size = parsed
			}
			attachment := Attachment{
				URL:         resolveURL(sourceURL, enclosure.URL),
				MIMEType:    enclosure.Type,
				SizeInBytes: size,
			}
			if normalized, ok := normalizeAttachment(attachment); ok {
				item.Attachments = append(item.Attachments, normalized)
			}
		}
		items = append(items, item)
	}
	return items, feed.Generator, nil
}

func looksLikeJSON(body []byte) bool {
	trimmed := bytes.TrimSpace(body)
	return len(trimmed) > 0 && trimmed[0] == '{'
}

func parseJSONFeed(body []byte, sourceURL string) ([]Item, bool, error) {
	var feed jsonFeed
	if err := json.Unmarshal(body, &feed); err != nil {
		return nil, false, fmt.Errorf("parse feed %q: %w", sourceURL, err)
	}
	switch feed.Version {
	case "https://jsonfeed.org/version/1", "https://jsonfeed.org/version/1.1":
	default:
		return nil, false, nil
	}
	info := FeedInfo{
		Title:       feed.Title,
		HomePageURL: resolveURL(sourceURL, feed.HomePageURL),
		FeedURL:     sourceURL,
	}
	feedAuthors := resolveAuthors(sourceURL, feed.Authors)
	if len(feedAuthors) == 0 && feed.Author != nil {
		feedAuthors = resolveAuthors(sourceURL, []Author{*feed.Author})
	}
	items := make([]Item, 0, len(feed.Items))
	for _, source := range feed.Items {
		itemURL := resolveURL(sourceURL, source.URL)
		id := firstNonEmpty(source.ID, itemURL)
		if id == "" {
			var err error
			id, err = generatedJSONFeedID(source)
			if err != nil {
				return nil, false, fmt.Errorf("generate item ID for feed %q: %w", sourceURL, err)
			}
		}
		authors := resolveAuthors(sourceURL, source.Authors)
		if len(authors) == 0 && source.Author != nil {
			authors = resolveAuthors(sourceURL, []Author{*source.Author})
		}
		if len(authors) == 0 {
			authors = append(authors, feedAuthors...)
		}
		item := Item{
			ID:            id,
			URL:           itemURL,
			ExternalURL:   resolveURL(sourceURL, source.ExternalURL),
			Title:         source.Title,
			ContentHTML:   source.ContentHTML,
			ContentText:   source.ContentText,
			Summary:       source.Summary,
			Image:         resolveURL(sourceURL, source.Image),
			BannerImage:   resolveURL(sourceURL, source.BannerImage),
			DatePublished: normalizeDate(source.DatePublished),
			DateModified:  normalizeDate(source.DateModified),
			Authors:       authors,
			Tags:          append([]string(nil), source.Tags...),
			Feed:          info,
		}
		for _, attachment := range source.Attachments {
			attachment := Attachment(attachment)
			attachment.URL = resolveURL(sourceURL, attachment.URL)
			if normalized, ok := normalizeAttachment(attachment); ok {
				item.Attachments = append(item.Attachments, normalized)
			}
		}

		items = append(items, item)
	}
	return items, true, nil
}

func resolveAuthors(base string, authors []Author) []Author {
	resolved := append([]Author(nil), authors...)
	for i := range resolved {
		resolved[i].URL = resolveURL(base, resolved[i].URL)
		resolved[i].Avatar = resolveURL(base, resolved[i].Avatar)
	}
	return resolved
}

func nextPageURL(body []byte, sourceURL string) (string, error) {
	body = bytes.TrimPrefix(body, []byte{0xef, 0xbb, 0xbf})
	if looksLikeJSON(body) {
		var feed struct {
			NextURL string `json:"next_url"`
		}
		if err := json.Unmarshal(body, &feed); err != nil {
			return "", err
		}
		return paginationURL(resolveURL(sourceURL, feed.NextURL)), nil
	}

	decoder := xml.NewDecoder(bytes.NewReader(body))
	decoder.CharsetReader = charset.NewReaderLabel
	var bases []string
	entryDepth := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		switch element := token.(type) {
		case xml.StartElement:
			base := sourceURL
			if len(bases) > 0 {
				base = bases[len(bases)-1]
			}
			for _, attribute := range element.Attr {
				if isXMLBase(attribute) {
					base = resolveURL(base, attribute.Value)
				}
			}
			bases = append(bases, base)
			if element.Name.Local == "entry" || element.Name.Local == "item" {
				entryDepth++
			}
			if entryDepth != 0 || element.Name.Space != atomNamespace || element.Name.Local != "link" {
				continue
			}
			var href string
			isNext := false
			for _, attribute := range element.Attr {
				switch strings.ToLower(attribute.Name.Local) {
				case "href":
					href = attribute.Value
				case "rel":
					for _, value := range strings.Fields(attribute.Value) {
						if strings.EqualFold(value, "next") {
							isNext = true
							break
						}
					}
				}
			}
			if isNext && href != "" {
				return paginationURL(resolveURL(base, href)), nil
			}
		case xml.EndElement:
			if element.Name.Local == "entry" || element.Name.Local == "item" {
				entryDepth--
			}
			bases = bases[:len(bases)-1]
		}
	}
}

func paginationURL(value string) string {
	parsed, err := url.Parse(value)
	if err != nil {
		return value
	}
	parsed.Fragment, parsed.RawFragment = "", ""
	return parsed.String()
}

func isWordPressGenerator(value string) bool {
	generator, err := url.Parse(strings.TrimSpace(value))
	if err != nil || generator.User != nil {
		return false
	}
	if !strings.EqualFold(generator.Scheme, "http") && !strings.EqualFold(generator.Scheme, "https") {
		return false
	}
	return strings.EqualFold(generator.Hostname(), "wordpress.org")
}

func currentWordPressPage(value string) int {
	parsed, err := url.Parse(value)
	if err != nil {
		return 1
	}
	page := 1
	if current, err := strconv.Atoi(parsed.Query().Get("paged")); err == nil && current > 0 {
		page = current
	}
	return page
}

func nextWordPressPageURL(value string, currentPage int) (string, int) {
	if currentPage < 1 || currentPage == int(^uint(0)>>1) {
		return "", currentPage
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "", currentPage
	}
	nextPage := currentPage + 1
	query := parsed.Query()
	query.Set("paged", strconv.Itoa(nextPage))
	parsed.RawQuery = query.Encode()
	return paginationURL(parsed.String()), nextPage
}

func isMissingWordPressPage(err error) bool {
	var statusErr *StatusError
	return errors.As(err, &statusErr) &&
		(statusErr.StatusCode == http.StatusNotFound || statusErr.StatusCode == http.StatusGone)
}

func normalizeAttachment(attachment Attachment) (Attachment, bool) {
	if attachment.URL == "" || attachment.MIMEType == "" {
		return Attachment{}, false
	}
	if attachment.SizeInBytes < 0 {
		attachment.SizeInBytes = 0
	}
	if attachment.DurationInSeconds < 0 {
		attachment.DurationInSeconds = 0
	}
	return attachment, true
}

func atomContent(entry *atom.Entry) (html, text string) {
	if entry == nil {
		return "", ""
	}
	if entry.Content == nil {
		return "", entry.Summary
	}
	contentType, _, err := mime.ParseMediaType(entry.Content.Type)
	if err != nil {
		contentType = entry.Content.Type
	}
	if isHTMLContentType(contentType) {
		return entry.Content.Value, ""
	}
	return "", entry.Content.Value
}

func isHTMLContentType(contentType string) bool {
	switch strings.ToLower(contentType) {
	case "html", "xhtml", "text/html", "application/xhtml+xml":
		return true
	default:
		return false
	}
}

func atomAuthors(base string, people []*atom.Person) []Author {
	authors := make([]Author, 0, len(people))
	for _, person := range people {
		if person == nil {
			continue
		}
		authors = append(authors, Author{Name: person.Name, URL: resolveURL(base, person.URI)})
	}
	return authors
}

func resolveURL(base, value string) string {
	if value == "" {
		return ""
	}
	baseURL, err := url.Parse(base)
	if err != nil {
		return value
	}
	relativeURL, err := url.Parse(value)
	if err != nil {
		return value
	}
	return baseURL.ResolveReference(relativeURL).String()
}

func displayURL(value string) string {
	parsed, err := url.Parse(value)
	if err != nil {
		return "<invalid URL>"
	}
	parsed.User = nil
	return parsed.String()
}

func generatedJSONFeedID(item jsonFeedItem) (string, error) {
	data, err := json.Marshal(item)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "urn:sha256:" + hex.EncodeToString(sum[:]), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func imageURL(image *gofeed.Image) string {
	if image == nil {
		return ""
	}
	return image.URL
}

func formatTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.Format(time.RFC3339Nano)
}

func normalizeDate(value string) string {
	if value == "" {
		return ""
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return ""
	}
	return parsed.Format(time.RFC3339Nano)
}

func parseTimeBound(value string, location *time.Location) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return &parsed, nil
	}
	parsed, err := time.ParseInLocation(time.DateOnly, value, location)
	if err != nil {
		return nil, fmt.Errorf("must be RFC3339 or YYYY-MM-DD")
	}
	return &parsed, nil
}

// filterDate reports the single date used to filter an item by period. The
// publication date takes precedence unless preferUpdated is set, and the other
// date is used when the preferred one is absent or unparseable.
func filterDate(item Item, preferUpdated bool) (time.Time, bool) {
	candidates := [2]string{item.DatePublished, item.DateModified}
	if preferUpdated {
		candidates = [2]string{item.DateModified, item.DatePublished}
	}
	for _, dateValue := range candidates {
		if itemTime, ok := parseItemDate(dateValue); ok {
			return itemTime, true
		}
	}
	return time.Time{}, false
}

func parseItemDate(value string) (time.Time, bool) {
	if value == "" {
		return time.Time{}, false
	}
	itemTime, err := time.Parse(time.RFC3339Nano, value)
	return itemTime, err == nil
}

func withinPeriod(item Item, since, until *time.Time, preferUpdated bool) bool {
	if since == nil && until == nil {
		return true
	}
	itemTime, ok := filterDate(item, preferUpdated)
	if !ok {
		return false
	}
	if since != nil && itemTime.Before(*since) {
		return false
	}
	if until != nil && !itemTime.Before(*until) {
		return false
	}
	return true
}

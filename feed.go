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

	"github.com/mmcdole/gofeed"
	"github.com/mmcdole/gofeed/atom"
	"golang.org/x/net/html"
	htmlatom "golang.org/x/net/html/atom"
	"golang.org/x/net/html/charset"
)

const userAgent = "rssnip/" + version
const maxFeedSize = 32 << 20
const defaultMaxPages = 10

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
	items := make([]Item, 0)
	seenItems := make(map[string]struct{})
	seenURLs := make(map[string]struct{})
	nextURL := feedURL
	for page := 0; page < maxPages && nextURL != ""; page++ {
		nextURL = paginationURL(nextURL)
		if _, ok := seenURLs[nextURL]; ok {
			break
		}
		seenURLs[nextURL] = struct{}{}

		pageItems, sourceURL, followingURL, err := fetchFeedPage(ctx, client, nextURL)
		if err != nil {
			return nil, err
		}
		seenURLs[paginationURL(sourceURL)] = struct{}{}
		for _, item := range pageItems {
			if _, ok := seenItems[item.ID]; ok {
				continue
			}
			seenItems[item.ID] = struct{}{}
			items = append(items, item)
		}
		nextURL = followingURL
	}
	return items, nil
}

func fetchFeedPage(ctx context.Context, client *http.Client, feedURL string) ([]Item, string, string, error) {
	body, sourceURL, contentType, err := fetchFeedDocument(ctx, client, feedURL)
	if err != nil {
		return nil, "", "", err
	}
	items, parseErr := parseFeed(body, sourceURL)
	if parseErr != nil {
		discoveredURL, ok := discoverFeedURL(body, sourceURL, contentType)
		if !ok {
			return nil, "", "", parseErr
		}
		body, sourceURL, _, err = fetchFeedDocument(ctx, client, discoveredURL)
		if err != nil {
			return nil, "", "", err
		}
		items, err = parseFeed(body, sourceURL)
		if err != nil {
			return nil, "", "", err
		}
	}
	nextURL, _ := nextPageURL(body, sourceURL)
	return items, sourceURL, nextURL, nil
}

func fetchFeedDocument(ctx context.Context, client *http.Client, feedURL string) ([]byte, string, string, error) {
	parsedURL, err := url.ParseRequestURI(feedURL)
	if err != nil || parsedURL.Host == "" ||
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
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/feed+json, application/json, application/atom+xml, application/rss+xml, application/rdf+xml, application/xml, text/xml, */*;q=0.1")

	resp, err := client.Do(req)
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
		return nil, "", "", fmt.Errorf("fetch %q: unexpected HTTP status %s", displayURL(feedURL), resp.Status)
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

// discoverFeedURL scans an HTML document for a feed link, without building a
// full DOM, to bound both memory and stack usage for adversarial input. It
// decodes the document using the charset declared by the HTTP Content-Type
// header or HTML metadata before scanning for <base> and <link> elements.
func discoverFeedURL(body []byte, sourceURL, contentType string) (string, bool) {
	if !looksLikeHTML(body, contentType) {
		return "", false
	}
	reader, err := charset.NewReader(bytes.NewReader(body), contentType)
	if err != nil {
		return "", false
	}
	tokenizer := html.NewTokenizer(reader)
	baseURL := sourceURL
	baseResolved := false
	for {
		switch tokenizer.Next() {
		case html.ErrorToken:
			return "", false
		case html.StartTagToken, html.SelfClosingTagToken:
			token := tokenizer.Token()
			switch token.DataAtom {
			case htmlatom.Base:
				if baseResolved {
					continue
				}
				href := strings.TrimSpace(tokenAttr(token, "href"))
				if href == "" {
					continue
				}
				resolved := resolveURL(sourceURL, href)
				if isDiscoverableFeedURL(resolved) {
					baseURL = resolved
					baseResolved = true
				}
			case htmlatom.Link:
				rel := strings.ToLower(tokenAttr(token, "rel"))
				isAlternate := hasRel(rel, "alternate")
				isFeed := hasRel(rel, "feed")
				if !isAlternate && !isFeed {
					continue
				}
				href := strings.TrimSpace(tokenAttr(token, "href"))
				if href == "" {
					continue
				}
				linkType := tokenAttr(token, "type")
				title := strings.ToLower(tokenAttr(token, "title"))
				if isFeed || isFeedMediaType(linkType) || looksLikeFeedPath(href) || strings.Contains(title, "rss") || strings.Contains(title, "atom") {
					resolved := resolveURL(baseURL, href)
					if isDiscoverableFeedURL(resolved) {
						return resolved, true
					}
				}
			}
		}
	}
}

func tokenAttr(token html.Token, name string) string {
	for _, attr := range token.Attr {
		if attr.Key == name {
			return attr.Val
		}
	}
	return ""
}

func looksLikeHTML(body []byte, contentType string) bool {
	if mediaType, _, err := mime.ParseMediaType(contentType); err == nil {
		switch strings.ToLower(mediaType) {
		case "text/html", "application/xhtml+xml":
			return true
		}
	}
	trimmed := bytes.TrimPrefix(bytes.TrimSpace(body), []byte("\xef\xbb\xbf"))
	lower := bytes.ToLower(trimmed)
	return bytes.HasPrefix(lower, []byte("<!doctype html")) ||
		bytes.HasPrefix(lower, []byte("<html")) ||
		bytes.HasPrefix(lower, []byte("<head")) ||
		bytes.HasPrefix(lower, []byte("<?xml-stylesheet"))
}

func hasRel(rel, want string) bool {
	for _, token := range strings.Fields(rel) {
		if token == want {
			return true
		}
	}
	return false
}

func isFeedMediaType(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = contentType
	}
	switch strings.ToLower(mediaType) {
	case "application/feed+json",
		"application/json",
		"application/atom+xml",
		"application/rss+xml",
		"application/rdf+xml",
		"application/xml",
		"text/xml":
		return true
	default:
		return false
	}
}

func looksLikeFeedPath(href string) bool {
	parsed, err := url.Parse(href)
	if err != nil {
		return false
	}
	candidate := parsed.Path
	if candidate == "" {
		candidate = href
	}
	lower := strings.ToLower(candidate)
	return strings.HasSuffix(lower, ".xml") ||
		strings.HasSuffix(lower, ".rss") ||
		strings.HasSuffix(lower, ".rdf") ||
		strings.HasSuffix(lower, ".atom") ||
		strings.HasSuffix(lower, ".json") ||
		strings.Contains(lower, "feed")
}

func isDiscoverableFeedURL(value string) bool {
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Host == "" {
		return false
	}
	if parsed.User != nil {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

func parseFeed(body []byte, sourceURL string) ([]Item, error) {
	body = bytes.TrimPrefix(body, []byte{0xef, 0xbb, 0xbf})
	if looksLikeJSON(body) {
		items, recognized, err := parseJSONFeed(body, sourceURL)
		if err != nil {
			return nil, err
		}
		if !recognized {
			return nil, fmt.Errorf("parse feed %q: JSON document is not a supported JSON Feed", sourceURL)
		}
		return items, nil
	}

	parser := gofeed.NewParser()
	parser.KeepOriginalFeed = true
	normalized, err := normalizeAtomDocument(body, sourceURL)
	if err != nil {
		return nil, fmt.Errorf("parse feed %q: %w", sourceURL, err)
	}
	feed, err := parser.Parse(bytes.NewReader(normalized))
	if err != nil {
		return nil, fmt.Errorf("parse feed %q: %w", sourceURL, err)
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
	return items, nil
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
		HomePageURL: feed.HomePageURL,
		FeedURL:     sourceURL,
	}
	feedAuthors := append([]Author(nil), feed.Authors...)
	if len(feedAuthors) == 0 && feed.Author != nil {
		feedAuthors = append(feedAuthors, *feed.Author)
	}
	items := make([]Item, 0, len(feed.Items))
	for _, source := range feed.Items {
		id := firstNonEmpty(source.ID, source.URL)
		if id == "" {
			var err error
			id, err = generatedJSONFeedID(source)
			if err != nil {
				return nil, false, fmt.Errorf("generate item ID for feed %q: %w", sourceURL, err)
			}
		}
		authors := append([]Author(nil), source.Authors...)
		if len(authors) == 0 && source.Author != nil {
			authors = append(authors, *source.Author)
		}
		if len(authors) == 0 {
			authors = append(authors, feedAuthors...)
		}
		item := Item{
			ID:            id,
			URL:           source.URL,
			ExternalURL:   source.ExternalURL,
			Title:         source.Title,
			ContentHTML:   source.ContentHTML,
			ContentText:   source.ContentText,
			Summary:       source.Summary,
			Image:         source.Image,
			BannerImage:   source.BannerImage,
			DatePublished: normalizeDate(source.DatePublished),
			DateModified:  normalizeDate(source.DateModified),
			Authors:       authors,
			Tags:          append([]string(nil), source.Tags...),
			Feed:          info,
		}
		for _, attachment := range source.Attachments {
			if normalized, ok := normalizeAttachment(Attachment(attachment)); ok {
				item.Attachments = append(item.Attachments, normalized)
			}
		}
		items = append(items, item)
	}
	return items, true, nil
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

func parseTimeBound(value string, endOfDay bool) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return &parsed, nil
	}
	parsed, err := time.Parse(time.DateOnly, value)
	if err != nil {
		return nil, fmt.Errorf("must be RFC3339 or YYYY-MM-DD")
	}
	if endOfDay {
		parsed = parsed.Add(24*time.Hour - time.Nanosecond)
	}
	return &parsed, nil
}

func withinPeriod(item Item, since, until *time.Time) bool {
	if since == nil && until == nil {
		return true
	}
	for _, dateValue := range []string{item.DatePublished, item.DateModified} {
		if dateValue == "" {
			continue
		}
		itemTime, err := time.Parse(time.RFC3339Nano, dateValue)
		if err != nil {
			continue
		}
		if since != nil && itemTime.Before(*since) {
			continue
		}
		if until != nil && itemTime.After(*until) {
			continue
		}
		return true
	}
	return false
}

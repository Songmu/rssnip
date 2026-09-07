package rssnip

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"
)

const userAgent = "rssnip/" + version
const maxFeedSize = 32 << 20

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
	URL               string `json:"url"`
	MIMEType          string `json:"mime_type"`
	Title             string `json:"title,omitempty"`
	SizeInBytes       int64  `json:"size_in_bytes,omitempty"`
	DurationInSeconds int64  `json:"duration_in_seconds,omitempty"`
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
	FeedURL     string         `json:"feed_url"`
	Items       []jsonFeedItem `json:"items"`
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
	URL               string `json:"url"`
	MIMEType          string `json:"mime_type"`
	Title             string `json:"title"`
	SizeInBytes       int64  `json:"size_in_bytes"`
	DurationInSeconds int64  `json:"duration_in_seconds"`
}

func fetchFeed(ctx context.Context, client *http.Client, feedURL string) ([]Item, error) {
	if _, err := url.ParseRequestURI(feedURL); err != nil {
		return nil, fmt.Errorf("invalid feed URL %q: %w", feedURL, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feedURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request for %q: %w", feedURL, err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/feed+json, application/atom+xml, application/rss+xml, application/rdf+xml, application/xml, text/xml")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %q: %w", feedURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return nil, fmt.Errorf("fetch %q: unexpected HTTP status %s", feedURL, resp.Status)
	}
	if resp.ContentLength > maxFeedSize {
		return nil, fmt.Errorf("read %q: feed exceeds %d MiB limit", feedURL, maxFeedSize>>20)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFeedSize+1))
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", feedURL, err)
	}
	if len(body) > maxFeedSize {
		return nil, fmt.Errorf("read %q: feed exceeds %d MiB limit", feedURL, maxFeedSize>>20)
	}
	return parseFeed(body, feedURL)
}

func parseFeed(body []byte, sourceURL string) ([]Item, error) {
	if looksLikeJSON(body) {
		items, recognized, err := parseJSONFeed(body, sourceURL)
		if err != nil {
			return nil, err
		}
		if recognized {
			return items, nil
		}
	}

	feed, err := gofeed.NewParser().Parse(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("parse feed %q: %w", sourceURL, err)
	}
	info := FeedInfo{
		Title:       feed.Title,
		HomePageURL: feed.Link,
		FeedURL:     sourceURL,
	}
	items := make([]Item, 0, len(feed.Items))
	for _, source := range feed.Items {
		item := Item{
			ID:          firstNonEmpty(source.GUID, source.Link, generatedID(source)),
			URL:         source.Link,
			Title:       source.Title,
			ContentHTML: source.Content,
			Summary:     source.Description,
			Image:       imageURL(source.Image),
			Tags:        append([]string(nil), source.Categories...),
			Feed:        info,
		}
		item.DatePublished = formatTime(source.PublishedParsed)
		item.DateModified = formatTime(source.UpdatedParsed)
		for _, person := range source.Authors {
			item.Authors = append(item.Authors, Author{
				Name: person.Name,
			})
		}
		for _, enclosure := range source.Enclosures {
			size, _ := strconv.ParseInt(enclosure.Length, 10, 64)
			item.Attachments = append(item.Attachments, Attachment{
				URL:         enclosure.URL,
				MIMEType:    enclosure.Type,
				SizeInBytes: size,
			})
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
	if !strings.HasPrefix(feed.Version, "https://jsonfeed.org/version/") {
		return nil, false, nil
	}
	info := FeedInfo{
		Title:       feed.Title,
		HomePageURL: feed.HomePageURL,
		FeedURL:     firstNonEmpty(feed.FeedURL, sourceURL),
	}
	items := make([]Item, 0, len(feed.Items))
	for _, source := range feed.Items {
		authors := append([]Author(nil), source.Authors...)
		if len(authors) == 0 && source.Author != nil {
			authors = append(authors, *source.Author)
		}
		item := Item{
			ID:            firstNonEmpty(source.ID, source.URL, generatedJSONFeedID(source)),
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
			item.Attachments = append(item.Attachments, Attachment(attachment))
		}
		items = append(items, item)
	}
	return items, true, nil
}

func generatedID(item *gofeed.Item) string {
	return hashID(item.Title, item.Published, item.Updated, item.Description)
}

func generatedJSONFeedID(item jsonFeedItem) string {
	return hashID(item.Title, item.DatePublished, item.DateModified, item.Summary)
}

func hashID(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "urn:sha256:" + hex.EncodeToString(sum[:])
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
		return value
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
	dateValue := firstNonEmpty(item.DatePublished, item.DateModified)
	if dateValue == "" {
		return false
	}
	itemTime, err := time.Parse(time.RFC3339Nano, dateValue)
	if err != nil {
		return false
	}
	if since != nil && itemTime.Before(*since) {
		return false
	}
	return until == nil || !itemTime.After(*until)
}

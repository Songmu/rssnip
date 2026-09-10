package feediscovery

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/url"
	"strings"
	"unicode"

	htmlatom "golang.org/x/net/html/atom"
)

// MaxDocumentSize is the maximum document size in bytes before decoding.
const MaxDocumentSize int64 = 32 << 20

// MaxNestingDepth bounds tracked HTML namespace/template frames and XML elements.
const MaxNestingDepth = 512

// ErrTooLarge indicates that a document exceeds MaxDocumentSize.
var ErrTooLarge = errors.New("feediscovery: document exceeds size limit")

// ErrTooDeep indicates that document nesting exceeds MaxNestingDepth.
var ErrTooDeep = errors.New("feediscovery: document nesting exceeds limit")

// Link is an advertised feed link, not a verified feed document.
type Link struct {
	// URL is the resolved absolute HTTP(S) URL, without a fragment.
	URL string
	// Title is the decoded title attribute, or empty if absent.
	Title string
	// Type is the decoded type attribute, including any MIME parameters.
	// It is empty if absent and is not inferred from the URL.
	Type string
}

// FindAll finds feed links in an HTML or XHTML document in document order.
// Duplicate URLs and the original title and type attribute values are retained.
// pageURL must be an absolute HTTP(S) document URL without userinfo, after any
// redirects. contentType is the document's HTTP Content-Type header; it may be
// empty when unknown. It is distinct from each link's Type.
//
// FindAll reads at most MaxDocumentSize+1 bytes from r and buffers the document
// for two passes. Tracked HTML namespace/template nesting and all XML element
// nesting are limited to MaxNestingDepth. It does not close r or fetch any URLs.
// No matches, including non-HTML input, return nil, nil. Read, size, depth,
// decoding, or scanning failures return nil and an error, never partial results.
func FindAll(r io.Reader, pageURL, contentType string) ([]Link, error) {
	if r == nil {
		return nil, errors.New("feediscovery: document reader is nil")
	}
	page, err := url.Parse(pageURL)
	if err != nil || !isDiscoverableFeedURL(pageURL) {
		return nil, errors.New("feediscovery: page URL must be absolute HTTP(S) without userinfo")
	}
	body, err := readDocument(r)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 || !looksLikeHTML(body, contentType) {
		return nil, nil
	}
	reader, err := discoveryReader(body, contentType)
	if err != nil {
		return nil, fmt.Errorf("decode document: %w", err)
	}
	tokenizer := newDiscoveryTokenizer(reader, contentType)
	baseURL, err := discoveryBaseURL(tokenizer, page)
	if err != nil {
		return nil, fmt.Errorf("scan document base: %w", err)
	}
	reader, err = discoveryReader(body, contentType)
	if err != nil {
		return nil, fmt.Errorf("decode document: %w", err)
	}
	tokenizer = newDiscoveryTokenizer(reader, contentType)
	sourceFetchURL := discoveryFetchURL(pageURL)
	templateDepth := 0
	var links []Link
	for {
		token, err := nextDocumentTag(tokenizer, &templateDepth)
		if err == io.EOF {
			return links, nil
		}
		if err != nil {
			return nil, fmt.Errorf("scan feed links: %w", err)
		}
		switch token.DataAtom {
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
			title := tokenAttr(token, "title")
			if isFeed || isFeedMediaType(linkType) || looksLikeFeedPath(href) || hasFeedHint(title, "rss", "atom") {
				resolved, ok := resolveCandidateURL(baseURL, href)
				if ok && resolved != sourceFetchURL {
					links = append(links, Link{URL: resolved, Title: title, Type: linkType})
				}
			}
		}
	}
}

// A document's base applies even to links that precede the base element.
func discoveryBaseURL(tokenizer *discoveryTokenizer, pageURL *url.URL) (*url.URL, error) {
	templateDepth := 0
	for {
		token, err := nextDocumentTag(tokenizer, &templateDepth)
		if err == io.EOF {
			return pageURL, nil
		}
		if err != nil {
			return nil, err
		}
		if token.DataAtom != htmlatom.Base {
			continue
		}
		href, present := tokenAttrValue(token, "href")
		if !present {
			continue
		}
		reference, err := url.Parse(strings.TrimSpace(href))
		if err != nil {
			return pageURL, nil
		}
		resolved := pageURL.ResolveReference(reference)
		if resolved.IsAbs() {
			return resolved, nil
		}
		return pageURL, nil
	}
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
	lower := strings.ToLower(parsed.Path)
	return strings.HasSuffix(lower, ".xml") ||
		strings.HasSuffix(lower, ".rss") ||
		strings.HasSuffix(lower, ".rdf") ||
		strings.HasSuffix(lower, ".atom") ||
		strings.HasSuffix(lower, ".json") ||
		hasFeedHint(lower, "feed", "rss", "atom")
}

func hasFeedHint(value string, hints ...string) bool {
	words := strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	for _, word := range words {
		for _, hint := range hints {
			if word == hint {
				return true
			}
		}
	}
	return false
}

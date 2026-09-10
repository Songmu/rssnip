rssnip
=======

[![Test Status](https://github.com/Songmu/rssnip/actions/workflows/test.yaml/badge.svg?branch=main)][actions]
[![Coverage Status](https://codecov.io/gh/Songmu/rssnip/branch/main/graph/badge.svg)][codecov]
[![MIT License](https://img.shields.io/github/license/Songmu/rssnip)][license]
[![PkgGoDev](https://pkg.go.dev/badge/github.com/Songmu/rssnip)][PkgGoDev]

[actions]: https://github.com/Songmu/rssnip/actions?workflow=test
[codecov]: https://codecov.io/gh/Songmu/rssnip
[license]: https://github.com/Songmu/rssnip/blob/main/LICENSE
[PkgGoDev]: https://pkg.go.dev/github.com/Songmu/rssnip

`rssnip` fetches RSS, Atom, RDF, and JSON Feed documents and writes their
entries as normalized JSON Feed 1.1 items. Its JSON Lines output is designed
for pipelines, crawlers, and further processing with tools such as `jq`.

## Synopsis

```console
% rssnip --since 2024-01-01 --until 2024-01-31 \
    https://example.com/feed.xml
{"id":"...","url":"https://example.com/article","title":"Example","date_published":"2024-01-15T00:00:00Z"}

% rssnip --jq '.url' -r https://example.com/feed.xml
https://example.com/article

% printf '%s\n' https://example.com/feed.xml | rssnip
{"id":"...","url":"https://example.com/article",...}

% rssnip --with-feed https://example.com/feed.xml
{"id":"...","url":"https://example.com/article","title":"Example","_feed":{"title":"Example Feed","feed_url":"https://example.com/feed.xml"}}

% rssnip --max-pages 3 https://example.com/feed.xml

% rssnip https://example.com/feed.xml https://example.org/feed.xml

% printf '%s\n' https://example.org/feed.xml | rssnip https://example.com/feed.xml

% rssnip --since 2024-01-01 --updated https://example.com/feed.xml
{"id":"...","url":"https://example.com/revised","title":"Revised","date_published":"2023-12-20T00:00:00Z","date_modified":"2024-01-10T00:00:00Z"}
```

## Description

`rssnip` supports:

- RSS 2.0
- Atom 1.0
- RDF/RSS 1.0
- JSON Feed 1.0 and 1.1
- Atom/RSS `rel="next"` and JSON Feed `next_url` pagination

Each item follows the JSON Feed 1.1 item shape. By default, items do not
include feed-level metadata. The `--with-feed` option adds the `_feed`
extension, recording the source feed's title, home page URL, and fetched feed
URL when items need to remain attributable across multiple feeds.

JSON Feed items use their supplied `id`, falling back to `url`. If both are
missing, a deterministic SHA-256 identifier is generated from the supported
item fields before normalization. Unknown fields (including custom extensions)
are ignored; changing a supported field changes the generated identifier.

Every item is emitted as one compact JSON object per line. Feed or blog/site
URLs may be supplied as positional arguments, one per line on standard input,
or by combining both forms. Positional URLs are processed first, followed by
standard input URLs; duplicate input URLs are preserved, and output preserves
feed and item order. Place all options before the first URL.
When a URL returns an HTML page instead of a feed, `rssnip` scans
`<link>` elements with `rel="alternate"` or `rel="feed"`. It selects the first
eligible feed candidate in document order and fetches that single feed, not
every linked feed. A `rel="alternate"` link qualifies through its media type,
a feed-like filename/path token, or a standalone RSS/Atom word in its title.
Fragments are removed from candidate URLs; links back to the same HTML document
are skipped. Hostnames and IPv6 addresses are lowercased for comparison, but
IPv6 zone identifiers retain their original spelling.
The first `<base href>` applies to all links, even when its value is empty.
If that base URL is malformed, links resolve against the page URL instead.
HTML decoding uses HTTP charset or HTML metadata; XHTML decoding uses the BOM,
HTTP charset, or XML declaration, defaulting to UTF-8. XHTML uses XML tokenization
and the XHTML namespace; template contents are ignored in both formats.
For HTML, links and bases in SVG/MathML are ignored unless they belong to HTML
content, such as links inside SVG `title` or `foreignObject` integration points.
If the selected URL fails to fetch or parse as a feed, the error is returned;
later candidates are not retried. Pagination of the selected feed follows the
same rules as a direct feed URL.
Discovery is limited to the initial supplied URL. Pagination responses must be
feeds; an HTML response on a later page is a parse error, not another discovery
opportunity.

Discovery scans the whole document before fetching a candidate. Decoding or
scanning failures are reported rather than treated as missing links. In
particular, malformed XHTML after an otherwise usable link now fails discovery;
earlier versions stopped at the first candidate and could miss such errors.

### Feed discovery library

The public `github.com/Songmu/rssnip/feediscovery` package finds all advertised
feed links without making HTTP requests or verifying feed contents:

```go
links, err := feediscovery.FindAll(
    strings.NewReader(document),
    "https://example.com/blog/",
    "text/html; charset=utf-8",
)
if err != nil {
    return err
}
for _, link := range links {
    fmt.Println(link.URL, link.Title, link.Type)
}
```

`pageURL` is the final document URL after redirects, used to resolve relative
links and bases and to exclude links back to the same document. `contentType`
is the document's HTTP Content-Type, including any charset; pass `""` when it
is unknown. An HTTP caller can pass `resp.Body`, `resp.Request.URL.String()`,
and `resp.Header.Get("Content-Type")`. The caller must close the body.

Results preserve document order, duplicate URLs, and each link's decoded
`title` and `type` attributes. `Link.Type` describes the advertised feed type,
not the HTML document's Content-Type. Missing attributes remain empty; types
are not inferred from filenames. The CLI still fetches only the first candidate.

No matches return `nil, nil`. Read, size, decoding, or scanning failures return
`nil, error`, never partial results. Oversized input can be identified with
`errors.Is(err, feediscovery.ErrTooLarge)`.

`FindAll` accepts an `io.Reader`, does not close it, and buffers the document for
two scanning passes. Its `MaxDocumentSize` is 32 MiB before decoding; it reads at
most one byte beyond the limit to detect overflow. This is not a total memory
limit. Passing existing bytes with `bytes.NewReader(body)` creates an additional
buffer inside the library. The caller controls network timeouts and cancellation.

Tracked HTML namespace/template frames and XML element nesting are limited to
`MaxNestingDepth` (512). Excessive nesting returns `ErrTooDeep`, which can be
identified with `errors.Is`; no partial candidates are returned. This bounds
parser state rather than imposing a depth limit on untracked ordinary HTML.

### Pagination

When a feed provides a standard next-page link, rssnip follows it and emits
pages in order. It supports Atom link relations (including Atom links embedded
in RSS) and JSON Feed `next_url`.

For an XML feed without an explicit next-page link, rssnip also recognizes a
WordPress feed when its feed-level `generator` is an HTTP(S) URL on
`wordpress.org`. It then preserves the feed URL's other query parameters and
tries successive `paged=2`, `paged=3`, and later URLs. Explicit pagination
always takes precedence; rssnip does not switch to guessed WordPress URLs after
an explicit pagination chain ends. A guessed page returning HTTP 404 or 410, or
a successfully parsed page that contributes no new item IDs, ends pagination
normally. This stops servers that accept `paged` but repeatedly return the same
content; partially overlapping pages continue when they contain at least one
new item. Other fetching and parsing failures remain errors.

It fetches at most 10 pages per supplied feed URL by default; use `--max-pages`
to choose another positive limit. Repeated page URLs stop pagination, and
duplicate item IDs across pages are emitted once.

When `--since` is present, rssnip can also stop before `--max-pages` after
observing at least five comparable item dates in newest-first order. The order
is checked continuously across page boundaries. Normal date filtering can use
either publication order, or modification order while every observed
publication date is no later than its modification date. With `--updated`, only
modification order can stop pagination. Items without a parseable publication
or modification date do not contribute to the five-item threshold.

This is intentionally a best-effort optimization based on the fetched prefix:
feed pagination does not guarantee that an unseen later page preserves the
observed order, so a matching item on such an out-of-order page may be omitted.

### Output schema

Without `--jq`, each JSON Lines record conforms to this JSON Schema:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "rssnip output item",
  "type": "object",
  "required": ["id"],
  "additionalProperties": false,
  "properties": {
    "id": { "type": "string" },
    "url": { "type": "string" },
    "external_url": { "type": "string" },
    "title": { "type": "string" },
    "content_html": { "type": "string" },
    "content_text": { "type": "string" },
    "summary": { "type": "string" },
    "image": { "type": "string" },
    "banner_image": { "type": "string" },
    "date_published": { "type": "string", "format": "date-time" },
    "date_modified": { "type": "string", "format": "date-time" },
    "authors": {
      "type": "array",
      "items": { "$ref": "#/$defs/author" }
    },
    "tags": {
      "type": "array",
      "items": { "type": "string" }
    },
    "attachments": {
      "type": "array",
      "items": { "$ref": "#/$defs/attachment" }
    },
    "_feed": { "$ref": "#/$defs/feed" }
  },
  "$defs": {
    "author": {
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "name": { "type": "string" },
        "url": { "type": "string" },
        "avatar": { "type": "string" }
      }
    },
    "attachment": {
      "type": "object",
      "required": ["url", "mime_type"],
      "additionalProperties": false,
      "properties": {
        "url": { "type": "string" },
        "mime_type": { "type": "string" },
        "title": { "type": "string" },
        "size_in_bytes": { "type": "integer" },
        "duration_in_seconds": { "type": "number" }
      }
    },
    "feed": {
      "type": "object",
      "required": ["feed_url"],
      "additionalProperties": false,
      "properties": {
        "title": { "type": "string" },
        "home_page_url": { "type": "string" },
        "feed_url": { "type": "string" }
      }
    }
  }
}
```

### Date filtering

`--since` and `--until` accept RFC 3339 timestamps or `YYYY-MM-DD` dates. Both
boundaries are inclusive. Date-only values are interpreted in UTC, and a
date-only `--until` includes the entire day. When neither boundary is given,
`--since` defaults to seven days before startup. Use `--all` to disable this
default and fetch all available items (up to `--max-pages`).

Each item is filtered by a single date. The publication date is preferred, and
the update date is used only when the publication date is missing or
unparseable. Passing `--updated` reverses that priority, so the update date is
preferred and the publication date becomes the fallback. This is useful for
picking up entries that were revised during the period even though they were
published earlier.

```console
% rssnip --since 2024-01-01 --updated https://example.com/feed.xml
```

The update date is `updated` in Atom and `date_modified` in JSON Feed. RSS 2.0
has no update date of its own, but a `dc:date` or an embedded `atom:updated`
element is recognized; an item carrying neither falls back to its publication
date. Items with no parseable date at all are omitted when a date filter is
active. Unparseable JSON Feed timestamps are omitted from normalized output
even when no date filter is active.

For ordered paginated feeds, `--since` uses the observed date order to avoid
fetching pages that cannot contain another matching item, as described under
[Pagination](#pagination). The page that crosses the boundary is processed
and checked in full before pagination stops, so a later ordering violation on
that page disables the optimization. An item dated exactly at `--since` remains
included.

### jq filtering

`--jq` applies a jq expression independently to each normalized item. The
expression may emit zero, one, or multiple results. `-r` writes string results
without JSON quoting and requires `--jq`.

```console
% rssnip --jq 'select(.tags | index("go")) | .url' -r \
    https://example.com/feed.xml
https://example.com/go-article
```

### Initial scope

The initial release focuses on direct feed retrieval and HTML feed discovery.
Conditional requests, persistent caching, and natural-language date expressions
are intentionally outside its scope.

## Installation

```console
# Install the latest version. (Install it into ./bin/ by default).
% curl -sfL https://raw.githubusercontent.com/Songmu/rssnip/main/install.sh | sh -s

# Specify installation directory ($(go env GOPATH)/bin/) and version.
% curl -sfL https://raw.githubusercontent.com/Songmu/rssnip/main/install.sh | sh -s -- -b $(go env GOPATH)/bin [vX.Y.Z]

# In alpine linux (as it does not come with curl by default)
% wget -O - -q https://raw.githubusercontent.com/Songmu/rssnip/main/install.sh | sh -s [vX.Y.Z]

# go install
% go install github.com/Songmu/rssnip/cmd/rssnip@latest
```

## Author

[Songmu](https://github.com/Songmu)

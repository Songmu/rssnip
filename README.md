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
    --url https://example.com/feed.xml
{"id":"...","url":"https://example.com/article","title":"Example","date_published":"2024-01-15T00:00:00Z","_feed":{"title":"Example Feed","feed_url":"https://example.com/feed.xml"}}

% rssnip --url https://example.com/feed.xml --jq '.url' -r
https://example.com/article
```

## Description

`rssnip` supports:

- RSS 2.0
- Atom 1.0
- RDF/RSS 1.0
- JSON Feed 1.0 and 1.1

Each item follows the JSON Feed 1.1 item shape. The `_feed` extension records
the source feed's title, home page URL, and fetched feed URL, which keeps items
attributable when multiple feeds are read at once.

JSON Feed items use their supplied `id`, falling back to `url`. If both are
missing, a deterministic SHA-256 identifier is generated from the supported
item fields before normalization. Unknown fields (including custom extensions)
are ignored; changing a supported field changes the generated identifier.

By default, every item is emitted as one compact JSON object per line. Use
`--json` to emit one JSON array instead. Feed URLs may be supplied by repeating `--url`, as positional arguments, one
per line on standard input, or by combining any of these forms; output
preserves feed and item order.

### Output schema

Without `--jq`, each JSON Lines record (or each `--json` array element)
conforms to this JSON Schema:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "rssnip output item",
  "type": "object",
  "required": ["id", "_feed"],
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
date-only `--until` includes the entire day. Items without a parseable
publication or modification date are omitted when a date filter is active. No
date filter is applied by default.
Unparseable JSON Feed timestamps are omitted from normalized output even when
no date filter is active.

### jq filtering

`--jq` applies a jq expression independently to each normalized item. The
expression may emit zero, one, or multiple results. `-r` writes string results
without JSON quoting and requires `--jq`.

```console
% rssnip --url https://example.com/feed.xml \
    --jq 'select(.tags | index("go")) | .url' -r
https://example.com/go-article
```

`--json` collects all jq results in its output array. `-r` and `--json` cannot
be combined.

### Initial scope

The initial release fetches explicit feed URLs. HTML feed discovery, feed
pagination, conditional requests, persistent caching, and natural-language
date expressions are intentionally outside its scope.

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

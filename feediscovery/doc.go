// Package feediscovery discovers advertised feed links in HTML and XHTML
// documents without fetching their URLs or parsing feed contents.
//
// FindAll returns every eligible link in document order, including duplicates.
// Links qualify through rel="feed", or rel="alternate" with a feed media type,
// path hint, or RSS/Atom title word. URLs are resolved against the first
// base[href] and the supplied document URL. Same-document links, non-HTTP(S)
// URLs, and URLs with userinfo are excluded. Template contents are ignored.
// HTML discovery distinguishes SVG/MathML content from HTML integration points,
// including SVG title elements and the MathML text integration exceptions.
//
// The document's Content-Type controls charset decoding and HTML versus XHTML
// tokenization. It may be empty when unknown. HTML metadata and XML declarations
// are handled according to the selected format; XHTML uses the XHTML namespace.
// Link.Type is instead the link element's declared type attribute, which is
// retained without validating or guessing the linked document's actual format.
//
// FindAll buffers up to MaxDocumentSize document bytes for two scanning passes.
// It reads one extra byte to detect oversized input. This byte limit is not a
// limit on total memory: decoding, tokenization, and returned links use additional
// memory. The caller owns the reader, including closing it and arranging any
// network timeouts or cancellation. No HTTP client is created by this package.
//
// A document with no matches returns nil, nil. Read, size, decoding, and scanning
// errors return no partial links. HTML syntax recovery is retained; this package
// is not a strict HTML validator. XHTML scanning errors anywhere in the document
// fail discovery, even when a usable candidate occurred before the error.
//
// Tracked HTML namespace/template frames and XML element nesting are limited to
// MaxNestingDepth (512). Excessive nesting returns ErrTooDeep without partial
// results. Ordinary HTML elements outside tracked contexts do not need a stack;
// this is a limit on parsing state, not validation of every HTML element's depth.
package feediscovery

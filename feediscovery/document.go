package feediscovery

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"mime"
	"strings"

	"golang.org/x/net/html"
	htmlatom "golang.org/x/net/html/atom"
	"golang.org/x/net/html/charset"
)

func readDocument(r io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, MaxDocumentSize+1))
	if err != nil {
		return nil, fmt.Errorf("read document: %w", err)
	}
	if int64(len(body)) > MaxDocumentSize {
		return nil, ErrTooLarge
	}
	return body, nil
}

func discoveryReader(body []byte, contentType string) (io.Reader, error) {
	mediaType, params, _ := mime.ParseMediaType(contentType)
	if mediaType != "application/xhtml+xml" {
		return charset.NewReader(bytes.NewReader(body), contentType)
	}
	// XML gives a BOM precedence over HTTP charset and the XML declaration.
	for _, bom := range [][]byte{{0xef, 0xbb, 0xbf}, {0xff, 0xfe}, {0xfe, 0xff}} {
		if bytes.HasPrefix(body, bom) {
			encoding, _, _ := charset.DetermineEncoding(body, "")
			return encoding.NewDecoder().Reader(bytes.NewReader(body)), nil
		}
	}
	if label := params["charset"]; label != "" {
		return charset.NewReaderLabel(label, bytes.NewReader(body))
	}
	label := "utf-8"
	if bytes.HasPrefix(body, []byte("<?xml")) {
		decoder := xml.NewDecoder(bytes.NewReader(body))
		decoder.CharsetReader = func(name string, input io.Reader) (io.Reader, error) {
			label = name
			return charset.NewReaderLabel(name, input)
		}
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
	}
	return charset.NewReaderLabel(label, bytes.NewReader(body))
}

type discoveryTokenizer struct {
	html     *html.Tokenizer
	xml      *xml.Decoder
	xmlDepth int

	// Track foreign content and its HTML descendants without building a DOM.
	namespaces []htmlNamespaceFrame
	openNames  map[string]int
}

func newDiscoveryTokenizer(reader io.Reader, contentType string) *discoveryTokenizer {
	mediaType, _, _ := mime.ParseMediaType(contentType)
	if mediaType != "application/xhtml+xml" {
		return &discoveryTokenizer{html: html.NewTokenizer(reader)}
	}
	decoder := xml.NewDecoder(reader)
	// discoveryReader already transcoded the original XML encoding to UTF-8.
	decoder.CharsetReader = func(_ string, input io.Reader) (io.Reader, error) {
		return input, nil
	}
	return &discoveryTokenizer{xml: decoder}
}

func (tokenizer *discoveryTokenizer) next() (html.TokenType, html.Token, error) {
	if tokenizer.html != nil {
		return tokenizer.nextHTML()
	}
	for {
		value, err := tokenizer.xml.Token()
		if err != nil {
			return html.ErrorToken, html.Token{}, err
		}
		var name xml.Name
		var attributes []xml.Attr
		kind := html.StartTagToken
		switch element := value.(type) {
		case xml.StartElement:
			if tokenizer.xmlDepth >= MaxNestingDepth {
				return html.ErrorToken, html.Token{}, ErrTooDeep
			}
			tokenizer.xmlDepth++
			name, attributes = element.Name, element.Attr
		case xml.EndElement:
			tokenizer.xmlDepth--
			name = element.Name
			kind = html.EndTagToken
		default:
			continue
		}
		if name.Space != "http://www.w3.org/1999/xhtml" {
			continue
		}
		token := html.Token{Type: kind, Data: name.Local, DataAtom: htmlatom.Lookup([]byte(name.Local))}
		for _, attribute := range attributes {
			if attribute.Name.Space == "" {
				token.Attr = append(token.Attr, html.Attribute{Key: attribute.Name.Local, Val: attribute.Value})
			}
		}
		return kind, token, nil
	}
}

func nextDocumentTag(tokenizer *discoveryTokenizer, templateDepth *int) (html.Token, error) {
	for {
		kind, token, err := tokenizer.next()
		if err != nil {
			return html.Token{}, err
		}
		switch kind {
		case html.StartTagToken, html.SelfClosingTagToken, html.EndTagToken:
			if token.DataAtom == htmlatom.Template {
				if kind == html.EndTagToken {
					if *templateDepth > 0 {
						*templateDepth--
					}
				} else {
					if *templateDepth >= MaxNestingDepth {
						return html.Token{}, ErrTooDeep
					}
					*templateDepth++
				}
				continue
			}
			if kind != html.EndTagToken && *templateDepth == 0 {
				return token, nil
			}
		}
	}
}

func tokenAttr(token html.Token, name string) string {
	value, _ := tokenAttrValue(token, name)
	return value
}

func tokenAttrValue(token html.Token, name string) (string, bool) {
	for _, attr := range token.Attr {
		if attr.Key == name {
			return attr.Val, true
		}
	}
	return "", false
}

func looksLikeHTML(body []byte, contentType string) bool {
	if mediaType, _, err := mime.ParseMediaType(contentType); err == nil {
		switch strings.ToLower(mediaType) {
		case "text/html", "application/xhtml+xml":
			return true
		}
	}
	trimmed := bytes.TrimSpace(bytes.TrimPrefix(bytes.TrimSpace(body), []byte("\xef\xbb\xbf")))
	tokenizer := html.NewTokenizer(bytes.NewReader(trimmed))
	for {
		switch tokenizer.Next() {
		case html.ErrorToken:
			return false
		case html.CommentToken:
			continue
		case html.TextToken:
			if len(bytes.TrimSpace(tokenizer.Text())) == 0 {
				continue
			}
			return false
		case html.DoctypeToken:
			return strings.EqualFold(tokenizer.Token().Data, "html")
		case html.StartTagToken, html.SelfClosingTagToken:
			switch tokenizer.Token().DataAtom {
			case htmlatom.Html, htmlatom.Head, htmlatom.Body, htmlatom.Title,
				htmlatom.Meta, htmlatom.Link, htmlatom.Base, htmlatom.Template,
				htmlatom.Script, htmlatom.Style, htmlatom.Noscript:
				return true
			default:
				return false
			}
		default:
			return false
		}
	}
}

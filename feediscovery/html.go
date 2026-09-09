package feediscovery

import (
	"strings"

	"golang.org/x/net/html"
	htmlatom "golang.org/x/net/html/atom"
)

type htmlNamespaceKind int8

const (
	htmlNamespaceHTML htmlNamespaceKind = iota
	htmlNamespaceSVG
	htmlNamespaceMathML
)

type htmlNamespaceFrame struct {
	name         string
	atom         htmlatom.Atom
	ns           htmlNamespaceKind
	integration  bool
	mathText     bool
	previous     int
	htmlBoundary int
}

func (tokenizer *discoveryTokenizer) nextHTML() (html.TokenType, html.Token, error) {
	for {
		tokenizer.html.AllowCDATA(tokenizer.currentHTMLFrame().ns != htmlNamespaceHTML)
		kind := tokenizer.html.Next()
		if kind == html.ErrorToken {
			return kind, html.Token{}, tokenizer.html.Err()
		}
		if kind != html.StartTagToken && kind != html.SelfClosingTagToken && kind != html.EndTagToken {
			return kind, html.Token{}, nil
		}
		token := tokenizer.html.Token()
		var ns htmlNamespaceKind
		if kind == html.EndTagToken {
			current := tokenizer.currentHTMLFrame()
			ns = current.ns
			if index, ok := tokenizer.openNames[token.Data]; ok {
				matched := tokenizer.namespaces[index]
				// HTML end tags cannot cross their foreign integration point.
				if current.ns != htmlNamespaceHTML || matched.ns == htmlNamespaceHTML || index >= current.htmlBoundary {
					ns = matched.ns
					tokenizer.popHTMLNamespaces(index)
				}
			}
		} else {
			ns = namespaceForStart(tokenizer.currentHTMLFrame(), token.DataAtom)
			if ns != htmlNamespaceHTML && isForeignBreakout(token) {
				for len(tokenizer.namespaces) > 0 {
					frame := tokenizer.currentHTMLFrame()
					if frame.ns == htmlNamespaceHTML || frame.integration || frame.mathText {
						break
					}
					tokenizer.popHTMLNamespaces(len(tokenizer.namespaces) - 1)
				}
				ns = htmlNamespaceHTML
			}
			if ns != htmlNamespaceHTML {
				// Foreign title/script/style elements do not enter HTML raw-text mode.
				tokenizer.html.NextIsNotRawText()
			}
			empty := ns == htmlNamespaceHTML && isHTMLVoid(token.DataAtom) ||
				ns != htmlNamespaceHTML && kind == html.SelfClosingTagToken
			if !empty && (ns != htmlNamespaceHTML || len(tokenizer.namespaces) > 0 || token.DataAtom == htmlatom.Template) {
				tokenizer.pushHTMLNamespace(newHTMLFrame(token, ns))
			}
		}
		if ns == htmlNamespaceHTML {
			return kind, token, nil
		}
	}
}

func (tokenizer *discoveryTokenizer) currentHTMLFrame() htmlNamespaceFrame {
	if n := len(tokenizer.namespaces); n > 0 {
		return tokenizer.namespaces[n-1]
	}
	return htmlNamespaceFrame{ns: htmlNamespaceHTML, htmlBoundary: -1}
}

func (tokenizer *discoveryTokenizer) pushHTMLNamespace(frame htmlNamespaceFrame) {
	if tokenizer.openNames == nil {
		tokenizer.openNames = make(map[string]int)
	}
	frame.htmlBoundary = -1
	if frame.ns == htmlNamespaceHTML {
		parent := tokenizer.currentHTMLFrame()
		frame.htmlBoundary = parent.htmlBoundary
		if parent.ns != htmlNamespaceHTML {
			frame.htmlBoundary = len(tokenizer.namespaces) - 1
		}
	}
	frame.previous = -1
	if index, ok := tokenizer.openNames[frame.name]; ok {
		frame.previous = index
	}
	tokenizer.openNames[frame.name] = len(tokenizer.namespaces)
	tokenizer.namespaces = append(tokenizer.namespaces, frame)
}

// Indexing the nearest matching name avoids rescanning a deep stack for every
// unmatched end tag. Each frame is pushed and popped at most once per pass.
func (tokenizer *discoveryTokenizer) popHTMLNamespaces(length int) {
	for i := len(tokenizer.namespaces) - 1; i >= length; i-- {
		frame := tokenizer.namespaces[i]
		if frame.previous < 0 {
			delete(tokenizer.openNames, frame.name)
		} else {
			tokenizer.openNames[frame.name] = frame.previous
		}
	}
	clear(tokenizer.namespaces[length:])
	tokenizer.namespaces = tokenizer.namespaces[:length]
}

func namespaceForStart(parent htmlNamespaceFrame, atom htmlatom.Atom) htmlNamespaceKind {
	htmlContent := parent.ns == htmlNamespaceHTML || parent.integration ||
		parent.mathText && atom != htmlatom.Mglyph && atom != htmlatom.Malignmark ||
		parent.ns == htmlNamespaceMathML && parent.atom == htmlatom.AnnotationXml && atom == htmlatom.Svg
	if !htmlContent {
		return parent.ns
	}
	switch atom {
	case htmlatom.Svg:
		return htmlNamespaceSVG
	case htmlatom.Math:
		return htmlNamespaceMathML
	default:
		return htmlNamespaceHTML
	}
}

func newHTMLFrame(token html.Token, ns htmlNamespaceKind) htmlNamespaceFrame {
	frame := htmlNamespaceFrame{name: token.Data, atom: token.DataAtom, ns: ns}
	switch ns {
	case htmlNamespaceSVG:
		switch token.DataAtom {
		case htmlatom.Foreignobject, htmlatom.Desc, htmlatom.Title:
			frame.integration = true
		}
	case htmlNamespaceMathML:
		switch token.DataAtom {
		case htmlatom.Mi, htmlatom.Mo, htmlatom.Mn, htmlatom.Ms, htmlatom.Mtext:
			frame.mathText = true
		case htmlatom.AnnotationXml:
			encoding := tokenAttr(token, "encoding")
			frame.integration = strings.EqualFold(encoding, "text/html") ||
				strings.EqualFold(encoding, "application/xhtml+xml")
		}
	}
	return frame
}

func isForeignBreakout(token html.Token) bool {
	switch token.DataAtom {
	case htmlatom.B, htmlatom.Big, htmlatom.Blockquote, htmlatom.Body, htmlatom.Br,
		htmlatom.Center, htmlatom.Code, htmlatom.Dd, htmlatom.Div, htmlatom.Dl,
		htmlatom.Dt, htmlatom.Em, htmlatom.Embed, htmlatom.H1, htmlatom.H2,
		htmlatom.H3, htmlatom.H4, htmlatom.H5, htmlatom.H6, htmlatom.Head,
		htmlatom.Hr, htmlatom.I, htmlatom.Img, htmlatom.Li, htmlatom.Listing,
		htmlatom.Menu, htmlatom.Meta, htmlatom.Nobr, htmlatom.Ol, htmlatom.P,
		htmlatom.Pre, htmlatom.Ruby, htmlatom.S, htmlatom.Small, htmlatom.Span,
		htmlatom.Strong, htmlatom.Strike, htmlatom.Sub, htmlatom.Sup, htmlatom.Table,
		htmlatom.Tt, htmlatom.U, htmlatom.Ul, htmlatom.Var:
		return true
	case htmlatom.Font:
		for _, attr := range token.Attr {
			switch attr.Key {
			case "color", "face", "size":
				return true
			}
		}
	}
	return false
}

func isHTMLVoid(atom htmlatom.Atom) bool {
	switch atom {
	case htmlatom.Area, htmlatom.Base, htmlatom.Basefont, htmlatom.Bgsound,
		htmlatom.Br, htmlatom.Col, htmlatom.Embed, htmlatom.Frame, htmlatom.Hr,
		htmlatom.Img, htmlatom.Input, htmlatom.Keygen, htmlatom.Link, htmlatom.Meta,
		htmlatom.Param, htmlatom.Source, htmlatom.Track, htmlatom.Wbr:
		return true
	default:
		return false
	}
}

package rssnip

import (
	"bytes"
	"encoding/xml"
	"io"
	"mime"
	"net/url"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"golang.org/x/net/html/charset"
)

const atomNamespace = "http://www.w3.org/2005/Atom"

// Resolve URIs before gofeed, whose resolver treats file bases as directories.
// Remove consumed xml:base attributes so gofeed cannot resolve them a second time.
func normalizeAtomDocument(body []byte, sourceURL string) ([]byte, error) {
	decoder := xml.NewDecoder(bytes.NewReader(body))
	decoder.CharsetReader = charset.NewReaderLabel
	var output bytes.Buffer
	encoder := xml.NewEncoder(&output)
	source, err := url.Parse(sourceURL)
	if err != nil {
		return nil, err
	}
	source.Fragment, source.RawFragment = "", ""
	bases := []string{source.String()}
	foundRoot := false
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch element := token.(type) {
		case xml.StartElement:
			if !foundRoot {
				if element.Name.Space != atomNamespace || element.Name.Local != "feed" {
					return body, nil
				}
				foundRoot = true
			}
			base := bases[len(bases)-1]
			for _, attribute := range element.Attr {
				if isXMLBase(attribute) {
					base = resolveURL(base, attribute.Value)
				}
			}
			attributes := make([]xml.Attr, 0, len(element.Attr))
			contentType := ""
			for _, attribute := range element.Attr {
				if isXMLBase(attribute) || attribute.Name.Space == "xmlns" ||
					(attribute.Name.Space == "" && attribute.Name.Local == "xmlns") {
					continue
				}
				if attribute.Name.Space == "" {
					if attribute.Name.Local == "type" {
						contentType = attribute.Value
					}
					if isURIAttribute(attribute.Name.Local) {
						attribute.Value = resolveURL(base, attribute.Value)
					}
				}
				attributes = append(attributes, attribute)
			}
			if element.Name.Space == "" {
				attributes = append(attributes, xml.Attr{Name: xml.Name{Local: "xmlns"}, Value: ""})
			}
			element.Attr = attributes
			if err := encoder.EncodeToken(element); err != nil {
				return nil, err
			}
			uriText := element.Name.Space == atomNamespace &&
				(element.Name.Local == "uri" || element.Name.Local == "icon" || element.Name.Local == "logo")
			mediaType, _, err := mime.ParseMediaType(contentType)
			if err != nil {
				mediaType = contentType
			}
			htmlText := element.Name.Space == atomNamespace &&
				(mediaType == "html" || mediaType == "text/html")
			if uriText || htmlText {
				var text string
				if err := decoder.DecodeElement(&text, &element); err != nil {
					return nil, err
				}
				if uriText {
					text = resolveURL(base, strings.TrimSpace(text))
				} else {
					text, err = resolveHTMLReferences(text, base)
					if err != nil {
						return nil, err
					}
				}
				if err := encoder.EncodeToken(xml.CharData(text)); err != nil {
					return nil, err
				}
				if err := encoder.EncodeToken(element.End()); err != nil {
					return nil, err
				}
				continue
			}
			bases = append(bases, base)
			continue
		case xml.EndElement:
			bases = bases[:len(bases)-1]
		case xml.ProcInst:
			if element.Target == "xml" {
				// Re-encoded tokens are UTF-8 regardless of the source encoding.
				continue
			}
		}
		if err := encoder.EncodeToken(token); err != nil {
			return nil, err
		}
	}
	if err := encoder.Flush(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func isXMLBase(attribute xml.Attr) bool {
	return attribute.Name.Space == "http://www.w3.org/XML/1998/namespace" && attribute.Name.Local == "base"
}

func isURIAttribute(name string) bool {
	switch name {
	case "href", "src", "uri", "scheme", "action", "background", "cite",
		"codebase", "data", "longdesc", "poster", "profile", "usemap":
		return true
	default:
		return false
	}
}

func resolveHTMLReferences(text, base string) (string, error) {
	nodes, err := html.ParseFragment(strings.NewReader(text), &html.Node{
		Type: html.ElementNode, Data: "div", DataAtom: atom.Div,
	})
	if err != nil {
		return "", err
	}
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		for i := range node.Attr {
			if isURIAttribute(node.Attr[i].Key) {
				node.Attr[i].Val = resolveURL(base, node.Attr[i].Val)
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	var output strings.Builder
	for _, node := range nodes {
		visit(node)
		if err := html.Render(&output, node); err != nil {
			return "", err
		}
	}
	return output.String(), nil
}

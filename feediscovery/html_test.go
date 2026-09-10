package feediscovery

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"golang.org/x/net/html"
	htmlatom "golang.org/x/net/html/atom"
)

func TestMathMLTextIntegrationNamespaces(t *testing.T) {
	t.Parallel()
	for _, parent := range []htmlatom.Atom{htmlatom.Mi, htmlatom.Mo, htmlatom.Mn, htmlatom.Ms, htmlatom.Mtext} {
		frame := newHTMLFrame(html.Token{DataAtom: parent, Data: parent.String()}, htmlNamespaceMathML)
		for _, child := range []htmlatom.Atom{htmlatom.Mglyph, htmlatom.Malignmark, htmlatom.Link, htmlatom.Span, htmlatom.Svg} {
			want := htmlNamespaceHTML
			switch child {
			case htmlatom.Mglyph, htmlatom.Malignmark:
				want = htmlNamespaceMathML
			case htmlatom.Svg:
				want = htmlNamespaceSVG
			}
			if got := namespaceForStart(frame, child); got != want {
				t.Errorf("%s > %s: namespace = %v, want %v", parent, child, got, want)
			}
		}
	}
}

func TestHTMLIntegrationNamespaces(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		parent   htmlatom.Atom
		ns       htmlNamespaceKind
		encoding string
		child    htmlatom.Atom
		want     htmlNamespaceKind
	}{
		{htmlatom.Title, htmlNamespaceSVG, "", htmlatom.Link, htmlNamespaceHTML},
		{htmlatom.Desc, htmlNamespaceSVG, "", htmlatom.Link, htmlNamespaceHTML},
		{htmlatom.Foreignobject, htmlNamespaceSVG, "", htmlatom.Math, htmlNamespaceMathML},
		{htmlatom.Mtext, htmlNamespaceSVG, "", htmlatom.Link, htmlNamespaceSVG},
		{htmlatom.Span, htmlNamespaceHTML, "", htmlatom.Mglyph, htmlNamespaceHTML},
		{htmlatom.AnnotationXml, htmlNamespaceMathML, "TEXT/HTML", htmlatom.Link, htmlNamespaceHTML},
		{htmlatom.AnnotationXml, htmlNamespaceMathML, "application/xhtml+xml", htmlatom.Link, htmlNamespaceHTML},
		{htmlatom.AnnotationXml, htmlNamespaceMathML, "text/xml", htmlatom.Link, htmlNamespaceMathML},
		{htmlatom.AnnotationXml, htmlNamespaceMathML, "text/xml", htmlatom.Svg, htmlNamespaceSVG},
	} {
		token := html.Token{DataAtom: tt.parent, Data: tt.parent.String(),
			Attr: []html.Attribute{{Key: "encoding", Val: tt.encoding}}}
		frame := newHTMLFrame(token, tt.ns)
		if got := namespaceForStart(frame, tt.child); got != tt.want {
			t.Errorf("%s (%v, %q) > %s: namespace = %v, want %v", tt.parent, tt.ns, tt.encoding, tt.child, got, tt.want)
		}
	}
}

func TestHTMLNamespaceStackRestoresNames(t *testing.T) {
	t.Parallel()
	tokenizer := newDiscoveryTokenizer(strings.NewReader(""), "text/html")
	for _, name := range []string{"svg", "unknown-a", "unknown-b", "unknown-a"} {
		if err := tokenizer.pushHTMLNamespace(htmlNamespaceFrame{name: name, ns: htmlNamespaceSVG}); err != nil {
			t.Fatal(err)
		}
	}
	tokenizer.popHTMLNamespaces(3)
	if index := tokenizer.openNames["unknown-a"]; index != 1 {
		t.Fatalf("previous matching name index = %d, want 1", index)
	}
	tokenizer.popHTMLNamespaces(1)
	if _, ok := tokenizer.openNames["unknown-b"]; ok {
		t.Fatal("popped name remains indexed")
	}
	tokenizer.popHTMLNamespaces(0)
	if len(tokenizer.namespaces) != 0 || len(tokenizer.openNames) != 0 {
		t.Fatal("popped stack retained namespace state")
	}
}

func TestHTMLNamespaceDeepUnmatchedEndTags(t *testing.T) {
	t.Parallel()
	body := "<html><svg>" + strings.Repeat("<g>", MaxNestingDepth-1) +
		strings.Repeat("</missing>", 4096) + `</svg><link rel="feed" href="/rss"></html>`
	tokenizer := newDiscoveryTokenizer(strings.NewReader(body), "text/html")
	var links int
	for {
		_, token, err := tokenizer.next()
		if err == io.EOF {
			break
		}

		if err != nil {
			t.Fatal(err)
		}
		if token.DataAtom == htmlatom.Link {
			links++
		}
	}
	if links != 1 || len(tokenizer.namespaces) != 0 || len(tokenizer.openNames) != 0 {
		t.Errorf("links = %d, frames = %d, names = %d", links, len(tokenizer.namespaces), len(tokenizer.openNames))
	}
}

func TestHTMLNamespaceDepthGuard(t *testing.T) {
	t.Parallel()
	tokenizer := newDiscoveryTokenizer(strings.NewReader(""), "text/html")
	for i := 0; i < MaxNestingDepth; i++ {
		err := tokenizer.pushHTMLNamespace(htmlNamespaceFrame{name: fmt.Sprintf("element-%d", i), ns: htmlNamespaceSVG})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := tokenizer.pushHTMLNamespace(htmlNamespaceFrame{name: "overflow", ns: htmlNamespaceSVG}); !errors.Is(err, ErrTooDeep) {
		t.Fatalf("overflow error = %v, want ErrTooDeep", err)
	}
	if len(tokenizer.namespaces) != MaxNestingDepth || len(tokenizer.openNames) != MaxNestingDepth {
		t.Errorf("overflow mutated state: frames = %d, names = %d", len(tokenizer.namespaces), len(tokenizer.openNames))
	}
}

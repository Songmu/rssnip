package feediscovery_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Songmu/rssnip/feediscovery"
)

func TestFindAllNestingLimit(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, contentType, root, outer, outerEnd, open, close string
		baseDepth                                             int
	}{
		{"SVG", "text/html", "<html>", "<svg>", "</svg>", "<g>", "</g>", 1},
		{"HTML templates", "text/html", "<html>", "", "", "<template>", "</template>", 0},
		{"HTML integration", "text/html", "<html>", "<svg><foreignObject>", "</foreignObject></svg>", "<div>", "</div>", 2},
		{"XHTML", "application/xhtml+xml", `<html xmlns="http://www.w3.org/1999/xhtml">`, "", "", "<div>", "</div>", 1},
		{"XML foreign namespace", "application/xhtml+xml", `<html xmlns="http://www.w3.org/1999/xhtml">`,
			`<svg xmlns="http://www.w3.org/2000/svg">`, "</svg>", "<g>", "</g>", 2},
		{"XML templates", "application/xhtml+xml", `<html xmlns="http://www.w3.org/1999/xhtml">`, "", "", "<template>", "</template>", 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			nested := func(depth int) string {
				count := depth - tt.baseDepth
				return tt.outer + strings.Repeat(tt.open, count) + strings.Repeat(tt.close, count) + tt.outerEnd
			}
			for _, base := range []string{"", `<base href="/"/>`} {
				for _, depth := range []int{feediscovery.MaxNestingDepth - 1, feediscovery.MaxNestingDepth, feediscovery.MaxNestingDepth + 1} {
					body := tt.root + base + `<link rel="feed" href="/rss"/>` +
						nested(depth) + "</html>"
					links, err := feediscovery.FindAll(strings.NewReader(body), "https://example.com/", tt.contentType)
					if depth <= feediscovery.MaxNestingDepth {
						if err != nil || len(links) != 1 || links[0].URL != "https://example.com/rss" {
							t.Errorf("depth %d, base %q: links = %#v, error = %v", depth, base, links, err)
						}
						continue
					}
					if !errors.Is(err, feediscovery.ErrTooDeep) || links != nil {
						t.Errorf("depth %d, base %q: links = %#v, error = %v, want nil, ErrTooDeep", depth, base, links, err)
					}
					stage := "scan document base"
					if base != "" {
						stage = "scan feed links"
					}
					if err != nil && !strings.Contains(err.Error(), stage) {
						t.Errorf("error = %v, want %q context", err, stage)
					}
				}
			}
			body := tt.root + `<link rel="feed" href="/rss"/>` +
				strings.Repeat(nested(feediscovery.MaxNestingDepth), 2) + "</html>"
			links, err := feediscovery.FindAll(strings.NewReader(body), "https://example.com/", tt.contentType)
			if err != nil || len(links) != 1 {
				t.Errorf("closed nesting was not reset: links = %#v, error = %v", links, err)
			}
		})
	}
}

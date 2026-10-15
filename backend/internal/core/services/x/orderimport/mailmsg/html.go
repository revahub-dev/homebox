package mailmsg

import (
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

var blockTags = map[string]bool{
	"br": true, "p": true, "div": true, "tr": true, "li": true, "h1": true, "h2": true,
	"h3": true, "h4": true, "table": true, "section": true, "hr": true,
}

var reProductURL = regexp.MustCompile(`https?://(?:www\.)?(?:amazon\.co\.jp/(?:[^\s"'<>]*/)?(?:dp|gp/product)/[A-Z0-9]{10}|item\.rakuten\.co\.jp/[^\s"'<>]+|(?:www\.)?yodobashi\.com/product/\d+)`)

// HTMLToText flattens HTML to text using a tokenizer only: no scripts are run,
// no remote content is fetched. Product links are kept on their own line
// after the anchor text so parsers can recover ASINs and item URLs.
func HTMLToText(src string) string {
	z := html.NewTokenizer(strings.NewReader(src))
	var b strings.Builder
	skip := 0
	var pendingLink string
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			return collapse(b.String())
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			tag := string(name)
			switch tag {
			case "script", "style", "head", "title":
				if tt == html.StartTagToken {
					skip++
				}
			case "td", "th":
				b.WriteString(" ")
			case "a":
				for hasAttr {
					var k, v []byte
					k, v, hasAttr = z.TagAttr()
					if string(k) == "href" && reProductURL.Match(v) {
						pendingLink = reProductURL.FindString(string(v))
					}
				}
			}
			if blockTags[tag] {
				b.WriteString("\n")
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			tag := string(name)
			switch tag {
			case "script", "style", "head", "title":
				if skip > 0 {
					skip--
				}
			case "a":
				if pendingLink != "" {
					b.WriteString("\n" + pendingLink + "\n")
					pendingLink = ""
				}
			}
			if blockTags[tag] {
				b.WriteString("\n")
			}
		case html.TextToken:
			if skip == 0 {
				b.WriteString(string(z.Text()))
			}
		case html.CommentToken, html.DoctypeToken:
			// ignored
		}
	}
}

var (
	reSpaces    = regexp.MustCompile(`[ \t\x{00a0}]+`)
	reBlankRuns = regexp.MustCompile(`\n\s*\n+`)
)

func collapse(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(reSpaces.ReplaceAllString(l, " "))
	}
	return strings.TrimSpace(reBlankRuns.ReplaceAllString(strings.Join(lines, "\n"), "\n"))
}

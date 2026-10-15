package mailmsg

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHTMLToTextDropsScriptsAndKeepsProductLinks(t *testing.T) {
	out := HTMLToText(`<html><head><title>t</title><style>p{}</style></head><body><script>evil()</script>
<p>Hello <b>world</b></p><a href="https://www.amazon.co.jp/foo/dp/B000000001/ref=x">Item</a><img src="https://track/p.gif">
<a href="https://example.com/unsubscribe">unsub</a></body></html>`)
	assert.NotContains(t, out, "evil")
	assert.NotContains(t, out, "p{}")
	assert.Contains(t, out, "Hello world")
	assert.Contains(t, out, "Item\nhttps://www.amazon.co.jp/foo/dp/B000000001")
	assert.NotContains(t, out, "example.com/unsubscribe")
}

func TestUnquote(t *testing.T) {
	assert.Equal(t, "a\nb", unquote("> a\n> b"))
	assert.Equal(t, "a\n> b\nc", unquote("a\n> b\nc"))
}

func TestParseLooseDate(t *testing.T) {
	d, ok := parseLooseDate("2026年10月13日 10:05")
	assert.True(t, ok)
	assert.Equal(t, "2026-10-13T10:05:00+09:00", d.Format("2006-01-02T15:04:05Z07:00"))
	_, ok = parseLooseDate("garbage")
	assert.False(t, ok)
}

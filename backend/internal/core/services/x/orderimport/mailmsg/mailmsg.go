// Package mailmsg turns raw MIME into the plain text the order parsers read.
// It decodes Japanese charsets, flattens HTML to text without running or
// fetching anything, and unwraps forwarded messages (inline or attached) to
// recover the original sender, date and Message-ID.
package mailmsg

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"golang.org/x/text/encoding/htmlindex"
	"golang.org/x/text/transform"
)

// maxDepth bounds recursion through nested multiparts / attached messages.
const maxDepth = 8

// Message is the parser-facing view of an email.
type Message struct {
	MessageID string // original Message-ID when recoverable, else the outer one
	OuterID   string // Message-ID of the envelope actually received
	From      string // address of the (outer) sender, i.e. the forwarder
	Subject   string
	Date      time.Time
	Text      string // body text of the original message (HTML flattened)

	Forwarded    bool
	OriginalFrom string // sender of the original message (store address)
	OriginalDate time.Time
}

var wordDecoder = &mime.WordDecoder{CharsetReader: charsetReader}

func charsetReader(charset string, input io.Reader) (io.Reader, error) {
	enc, err := htmlindex.Get(strings.ToLower(strings.TrimSpace(charset)))
	if err != nil {
		return nil, fmt.Errorf("unsupported charset %q", charset)
	}
	return transform.NewReader(input, enc.NewDecoder()), nil
}

func decodeHeader(s string) string {
	out, err := wordDecoder.DecodeHeader(s)
	if err != nil {
		return s
	}
	return out
}

func addressOf(s string) string {
	s = decodeHeader(s)
	if a, err := mail.ParseAddress(s); err == nil {
		return strings.ToLower(a.Address)
	}
	if m := reAngleAddr.FindStringSubmatch(s); m != nil {
		return strings.ToLower(m[1])
	}
	return strings.ToLower(strings.TrimSpace(s))
}

var reAngleAddr = regexp.MustCompile(`<([^<>@\s]+@[^<>\s]+)>`)

// Parse reads raw MIME bytes.
func Parse(raw []byte) (*Message, error) {
	return parse(raw, 0)
}

func parse(raw []byte, depth int) (*Message, error) {
	if depth > maxDepth {
		return nil, errors.New("message nesting too deep")
	}
	m, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("not a valid email: %w", err)
	}
	if m.Header.Get("From") == "" && m.Header.Get("Subject") == "" && m.Header.Get("Message-Id") == "" {
		return nil, errors.New("not a valid email: missing headers")
	}

	out := &Message{
		OuterID: strings.TrimSpace(m.Header.Get("Message-Id")),
		From:    addressOf(m.Header.Get("From")),
		Subject: strings.TrimSpace(decodeHeader(m.Header.Get("Subject"))),
	}
	out.MessageID = out.OuterID
	if d, err := m.Header.Date(); err == nil {
		out.Date = d
	}

	var parts collected
	if err := walk(textproto(m.Header), m.Body, &parts, depth); err != nil {
		return nil, err
	}

	switch {
	case parts.attached != nil:
		// Forwarded as attachment: the attached message is the original.
		inner := parts.attached
		out.Forwarded = true
		out.MessageID = firstNonEmpty(inner.MessageID, out.OuterID)
		out.OriginalFrom = firstNonEmpty(inner.OriginalFrom, inner.From)
		out.OriginalDate = inner.Date
		if !inner.OriginalDate.IsZero() {
			out.OriginalDate = inner.OriginalDate
		}
		out.Text = inner.Text
	default:
		// Prefer the HTML part: it keeps product links next to their items.
		body := parts.plain
		if parts.html != "" {
			body = HTMLToText(parts.html)
		}
		out.Text = normalizeNewlines(body)
		unwrapInline(out)
	}
	if out.OriginalFrom == "" {
		out.OriginalFrom = out.From
	}
	if out.OriginalDate.IsZero() {
		out.OriginalDate = out.Date
	}
	return out, nil
}

type collected struct {
	plain, html string
	attached    *Message
}

type header interface{ Get(string) string }

func textproto(h mail.Header) header { return h }

func walk(h header, body io.Reader, c *collected, depth int) error {
	ctype := h.Get("Content-Type")
	if ctype == "" {
		ctype = "text/plain; charset=us-ascii"
	}
	mediaType, params, err := mime.ParseMediaType(ctype)
	if err != nil {
		mediaType, params = "text/plain", map[string]string{}
	}
	data, err := io.ReadAll(decodeTransfer(h.Get("Content-Transfer-Encoding"), body))
	if err != nil {
		return err
	}

	switch {
	case strings.HasPrefix(mediaType, "multipart/"):
		mr := multipart.NewReader(bytes.NewReader(data), params["boundary"])
		for {
			p, err := mr.NextPart()
			if errors.Is(err, io.EOF) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("multipart: %w", err)
			}
			if err := walk(p.Header, p, c, depth+1); err != nil {
				return err
			}
		}
	case mediaType == "message/rfc822":
		if c.attached == nil {
			inner, err := parse(data, depth+1)
			if err == nil {
				c.attached = inner
			}
		}
	case mediaType == "text/plain" && c.plain == "" && !isAttachment(h):
		c.plain = decodeCharset(data, params["charset"])
	case mediaType == "text/html" && c.html == "" && !isAttachment(h):
		c.html = decodeCharset(data, params["charset"])
	}
	return nil
}

func isAttachment(h header) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(h.Get("Content-Disposition"))), "attachment")
}

func decodeTransfer(enc string, r io.Reader) io.Reader {
	switch strings.ToLower(strings.TrimSpace(enc)) {
	case "base64":
		return base64.NewDecoder(base64.StdEncoding, &stripWS{r: r})
	case "quoted-printable":
		return quotedprintable.NewReader(r)
	default:
		return r
	}
}

// stripWS drops CR/LF/space so base64 bodies with line breaks decode.
type stripWS struct{ r io.Reader }

func (s *stripWS) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	j := 0
	for i := 0; i < n; i++ {
		switch p[i] {
		case '\r', '\n', ' ', '\t':
		default:
			p[j] = p[i]
			j++
		}
	}
	if j == 0 && n > 0 && err == nil {
		return s.Read(p)
	}
	return j, err
}

func decodeCharset(b []byte, charset string) string {
	charset = strings.ToLower(strings.TrimSpace(charset))
	if charset == "" || charset == "utf-8" || charset == "us-ascii" {
		return string(b)
	}
	enc, err := htmlindex.Get(charset)
	if err != nil {
		return string(b)
	}
	out, _, err := transform.Bytes(enc.NewDecoder(), b)
	if err != nil {
		return string(b)
	}
	return string(out)
}

func normalizeNewlines(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

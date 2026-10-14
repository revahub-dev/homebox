package mailmsg

import (
	"net/mail"
	"regexp"
	"strings"
	"time"
)

// Markers that introduce an inline-forwarded message (Gmail, Outlook,
// Thunderbird, Apple Mail; English and Japanese UIs).
var reForwardMarker = regexp.MustCompile(`(?m)^\s*(?:-{2,}\s*(?:Forwarded message|Original Message|転送メッセージ|元のメッセージ)\s*-{2,}|Begin forwarded message:|転送されたメッセージ[:：]?)\s*$`)

var reHeaderLine = regexp.MustCompile(`^\s*>?\s*(From|差出人|送信者|Date|日付|送信日時|Sent|Subject|件名|To|宛先|Cc)\s*[:：]\s*(.*)$`)

// unwrapInline replaces m.Text with the forwarded original when a forward
// marker is present, and records the original sender and date.
func unwrapInline(m *Message) {
	loc := reForwardMarker.FindStringIndex(m.Text)
	if loc == nil {
		return
	}
	rest := m.Text[loc[1]:]
	lines := strings.Split(rest, "\n")
	i := 0
	for ; i < len(lines) && strings.TrimSpace(strings.TrimLeft(lines[i], "> ")) == ""; i++ {
	}
	seen := false
	for ; i < len(lines); i++ {
		l := lines[i]
		mm := reHeaderLine.FindStringSubmatch(l)
		if mm == nil {
			if strings.TrimSpace(strings.TrimLeft(l, ">")) == "" && seen {
				i++
				break
			}
			if seen {
				break
			}
			continue
		}
		seen = true
		val := strings.TrimSpace(mm[2])
		switch mm[1] {
		case "From", "差出人", "送信者":
			m.OriginalFrom = addressOf(val)
		case "Date", "日付", "送信日時", "Sent":
			if t, ok := parseLooseDate(val); ok {
				m.OriginalDate = t
			}
		}
	}
	body := strings.Join(lines[min(i, len(lines)):], "\n")
	m.Text = unquote(body)
	m.Forwarded = true
}

// unquote strips "> " prefixes when most non-empty lines are quoted.
func unquote(s string) string {
	lines := strings.Split(s, "\n")
	quoted, nonEmpty := 0, 0
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		nonEmpty++
		if strings.HasPrefix(strings.TrimLeft(l, " "), ">") {
			quoted++
		}
	}
	if nonEmpty == 0 || quoted*2 < nonEmpty {
		return s
	}
	for i, l := range lines {
		l = strings.TrimLeft(l, " ")
		l = strings.TrimPrefix(l, ">")
		lines[i] = strings.TrimPrefix(l, " ")
	}
	return strings.Join(lines, "\n")
}

var reJPDate = regexp.MustCompile(`(\d{4})[年/.-](\d{1,2})[月/.-](\d{1,2})日?(?:\D+(\d{1,2}):(\d{2}))?`)

func parseLooseDate(s string) (time.Time, bool) {
	if t, err := mail.ParseDate(s); err == nil {
		return t, true
	}
	for _, layout := range []string{
		"Mon, Jan 2, 2006 at 3:04 PM", "Mon, Jan 2, 2006 at 15:04", "January 2, 2006 at 3:04:05 PM MST",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	if m := reJPDate.FindStringSubmatch(s); m != nil {
		return jpDate(m), true
	}
	return time.Time{}, false
}

var jst = time.FixedZone("JST", 9*3600)

func jpDate(m []string) time.Time {
	atoi := func(s string) int {
		n := 0
		for _, r := range s {
			n = n*10 + int(r-'0')
		}
		return n
	}
	hh, mi := 0, 0
	if len(m) > 5 && m[4] != "" {
		hh, mi = atoi(m[4]), atoi(m[5])
	}
	return time.Date(atoi(m[1]), time.Month(atoi(m[2])), atoi(m[3]), hh, mi, 0, 0, jst)
}

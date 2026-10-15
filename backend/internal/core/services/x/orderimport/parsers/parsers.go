// Package parsers holds the deterministic order-email parsers (Amazon JP,
// Rakuten, Yodobashi) and the Order type shared with the LLM extractor.
package parsers

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/unicode/norm"

	"github.com/sysadminsmedia/homebox/backend/internal/core/services/x/orderimport/mailmsg"
)

// Store identifiers.
const (
	StoreAmazonJP  = "amazonjp"
	StoreRakuten   = "rakuten"
	StoreYodobashi = "yodobashi"
)

// LineItem is one purchased product.
type LineItem struct {
	Name        string  `json:"name"`
	Quantity    float64 `json:"quantity"`
	UnitPrice   float64 `json:"unit_price"`
	ASIN        string  `json:"asin"`
	JAN         string  `json:"jan"`
	ModelNumber string  `json:"model_number"`
	URL         string  `json:"url"`
}

// Order is the normalized result of any extractor.
type Order struct {
	Store       string     `json:"store"`
	OrderNumber string     `json:"order_number"`
	OrderDate   *time.Time `json:"-"`
	Total       float64    `json:"total"`
	Currency    string     `json:"currency"`
	Items       []LineItem `json:"items"`
}

// ErrNoItems is returned when a parser recognised the store but found nothing.
var ErrNoItems = errors.New("no line items found")

// Parser is a deterministic store parser.
type Parser interface {
	Store() string
	// Match reports whether the message looks like this store's order mail.
	Match(m *mailmsg.Message) bool
	Parse(m *mailmsg.Message) (*Order, error)
}

// All returns the built-in parsers in match order.
func All() []Parser {
	return []Parser{amazonJP{}, rakuten{}, yodobashi{}}
}

// Detect returns the first parser matching the message, or nil.
func Detect(m *mailmsg.Message) Parser {
	for _, p := range All() {
		if p.Match(m) {
			return p
		}
	}
	return nil
}

// DetectStore guesses the store from sender domain or body markers, for
// labelling messages the parsers could not handle.
func DetectStore(m *mailmsg.Message) string {
	if p := Detect(m); p != nil {
		return p.Store()
	}
	return ""
}

// --- shared helpers ---------------------------------------------------------

// nfkc folds full-width digits/punctuation so regexes can stay ASCII.
func nfkc(s string) string { return norm.NFKC.String(s) }

func senderDomainIs(m *mailmsg.Message, domains ...string) bool {
	for _, from := range []string{m.OriginalFrom, m.From} {
		at := strings.LastIndexByte(from, '@')
		if at < 0 {
			continue
		}
		host := from[at+1:]
		for _, d := range domains {
			if host == d || strings.HasSuffix(host, "."+d) {
				return true
			}
		}
	}
	return false
}

var reMoney = regexp.MustCompile(`[¥￥]\s*([0-9][0-9,]*)|([0-9][0-9,]*)\s*(?:円|\(円\))`)

func parseMoney(s string) (float64, bool) {
	m := reMoney.FindStringSubmatch(nfkc(s))
	if m == nil {
		return 0, false
	}
	v := m[1]
	if v == "" {
		v = m[2]
	}
	f, err := strconv.ParseFloat(strings.ReplaceAll(v, ",", ""), 64)
	return f, err == nil
}

var reDate = regexp.MustCompile(`(\d{4})\s*[年/.-]\s*(\d{1,2})\s*[月/.-]\s*(\d{1,2})`)

var jst = time.FixedZone("JST", 9*3600)

func parseDate(s string) *time.Time {
	m := reDate.FindStringSubmatch(nfkc(s))
	if m == nil {
		return nil
	}
	y, _ := strconv.Atoi(m[1])
	mo, _ := strconv.Atoi(m[2])
	d, _ := strconv.Atoi(m[3])
	if mo < 1 || mo > 12 || d < 1 || d > 31 {
		return nil
	}
	t := time.Date(y, time.Month(mo), d, 0, 0, 0, 0, jst)
	return &t
}

// findAfterLabel returns the text after the first matching label regex.
func findAfterLabel(text string, re *regexp.Regexp) string {
	m := re.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[len(m)-1])
}

func lines(text string) []string {
	raw := strings.Split(nfkc(text), "\n")
	out := make([]string, 0, len(raw))
	for _, l := range raw {
		out = append(out, strings.TrimSpace(l))
	}
	return out
}

// reTrailingModel picks "[HEG-S-KAAAA]" style model numbers off a title.
var reTrailingModel = regexp.MustCompile(`[\[［]([A-Za-z0-9][A-Za-z0-9\-_/.]{1,40})[\]］]\s*$`)

func modelFromTitle(title string) string {
	if m := reTrailingModel.FindStringSubmatch(nfkc(title)); m != nil {
		return m[1]
	}
	return ""
}

var reJAN = regexp.MustCompile(`JAN(?:コード)?\s*[:：]?\s*(\d{13}|\d{8})(?:\D|$)`)

func clampQty(q float64) float64 {
	if q <= 0 {
		return 1
	}
	return q
}

package parsers

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/sysadminsmedia/homebox/backend/internal/core/services/x/orderimport/mailmsg"
)

// yodobashi parses Yodobashi.com "ご注文ありがとうございます" mails:
//
//	【ご注文番号】 1234567890
//	【ご注文日】 2026年10月10日
//	・商品名：Nintendo Switch（有機ELモデル） ホワイト [HEG-S-KAAAA]
//	  数量：1
//	  単価：37,980円
type yodobashi struct{}

var (
	reYdbOrderNo = regexp.MustCompile(`[【\[]\s*(?:ご注文番号|注文番号)\s*[】\]]\s*[:：]?\s*([0-9A-Za-z-]+)`)
	reYdbDate    = regexp.MustCompile(`[【\[]\s*(?:ご注文日|注文日|ご注文日時)\s*[】\]]\s*[:：]?\s*(.+)`)
	reYdbTotal   = regexp.MustCompile(`[【\[]\s*(?:お支払い合計|ご請求金額|合計金額)\s*[】\]]\s*[:：]?\s*([0-9,]+\s*円)`)
	reYdbName    = regexp.MustCompile(`^[・･]?\s*(?:商品名)\s*[:：]\s*(.+)$|^[・･]?\s*「(.+)」$`)
	reYdbQty     = regexp.MustCompile(`^(?:数量|個数)\s*[:：]\s*([0-9]+)`)
	reYdbPrice   = regexp.MustCompile(`^(?:単価|価格|販売価格)\s*[:：]\s*(.+円)`)
	reYdbModel   = regexp.MustCompile(`^(?:メーカー品番|型番|品番)\s*[:：]\s*(\S+)`)
	reYdbURL     = regexp.MustCompile(`https?://(?:www\.)?yodobashi\.com/product/\d+/?`)
)

func (yodobashi) Store() string { return StoreYodobashi }

func (yodobashi) Match(m *mailmsg.Message) bool {
	if senderDomainIs(m, "yodobashi.com") {
		return true
	}
	t := nfkc(m.Text)
	return strings.Contains(t, "ヨドバシ") && reYdbOrderNo.MatchString(t)
}

func (yodobashi) Parse(m *mailmsg.Message) (*Order, error) {
	text := nfkc(m.Text)
	o := &Order{Store: StoreYodobashi, Currency: "JPY"}
	o.OrderNumber = findAfterLabel(text, reYdbOrderNo)
	if d := findAfterLabel(text, reYdbDate); d != "" {
		o.OrderDate = parseDate(d)
	}
	if o.OrderDate == nil && !m.OriginalDate.IsZero() {
		t := m.OriginalDate.In(jst)
		o.OrderDate = &t
	}
	if t := findAfterLabel(text, reYdbTotal); t != "" {
		o.Total, _ = parseMoney(t)
	}

	ls := lines(text)
	var cur *LineItem
	flush := func() {
		if cur != nil && cur.Name != "" {
			cur.Quantity = clampQty(cur.Quantity)
			if cur.ModelNumber == "" {
				cur.ModelNumber = modelFromTitle(cur.Name)
			}
			o.Items = append(o.Items, *cur)
		}
		cur = nil
	}
	for _, l := range ls {
		if nm := reYdbName.FindStringSubmatch(l); nm != nil {
			flush()
			name := nm[1]
			if name == "" {
				name = nm[2]
			}
			cur = &LineItem{Name: strings.TrimSpace(name)}
			continue
		}
		if cur == nil {
			continue
		}
		switch {
		case reYdbQty.MatchString(l):
			cur.Quantity, _ = strconv.ParseFloat(reYdbQty.FindStringSubmatch(l)[1], 64)
		case reYdbPrice.MatchString(l):
			cur.UnitPrice, _ = parseMoney(reYdbPrice.FindStringSubmatch(l)[1])
		case reYdbModel.MatchString(l):
			cur.ModelNumber = reYdbModel.FindStringSubmatch(l)[1]
		case reJAN.MatchString(l):
			cur.JAN = reJAN.FindStringSubmatch(l)[1]
		case reYdbURL.MatchString(l):
			cur.URL = reYdbURL.FindString(l)
		case strings.HasPrefix(l, "【") || strings.HasPrefix(l, "---"):
			flush()
		}
	}
	flush()
	if len(o.Items) == 0 {
		return o, ErrNoItems
	}
	return o, nil
}

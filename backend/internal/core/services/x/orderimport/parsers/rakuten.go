package parsers

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/sysadminsmedia/homebox/backend/internal/core/services/x/orderimport/mailmsg"
)

// rakuten parses Rakuten Ichiba "【楽天市場】注文内容ご確認" mails:
//
//	[受注番号] 123456-20261010-0012345678
//	[日時] 2026-10-10 12:34:56
//	[商品]
//	象印 マイコン沸とう電動ポット 2.2L CD-WU22-WG
//	価格 12,800(円) x 1(個) = 12,800(円)
type rakuten struct{}

var (
	reRakOrderNo = regexp.MustCompile(`[\[［【]\s*(?:受注番号|注文番号)\s*[\]］】]\s*[:：]?\s*([0-9]+-[0-9]+-[0-9]+)`)
	reRakDate    = regexp.MustCompile(`[\[［【]\s*(?:日時|注文日時|注文日)\s*[\]］】]\s*[:：]?\s*(.+)`)
	reRakTotal   = regexp.MustCompile(`(?:合計金額|お支払い金額|支払い金額)\s*(?:\(税込\))?\s*[:：]?\s*([0-9,]+\s*\(?円\)?)`)
	reRakLine    = regexp.MustCompile(`^価格\s*[:：]?\s*([0-9,]+)\s*\(?円\)?\s*[x×*]\s*([0-9]+)\s*\(?個\)?`)
	reRakURL     = regexp.MustCompile(`https?://item\.rakuten\.co\.jp/\S+`)
	reRakSkip    = regexp.MustCompile(`^(?:[\[［][^\]］]*[\]］]|https?://|価格|小計|送料|ポイント|-{3,}|={3,}|$)`)
)

func (rakuten) Store() string { return StoreRakuten }

func (rakuten) Match(m *mailmsg.Message) bool {
	if senderDomainIs(m, "rakuten.co.jp") {
		return true
	}
	t := nfkc(m.Text)
	return strings.Contains(t, "楽天市場") && reRakOrderNo.MatchString(t)
}

func (rakuten) Parse(m *mailmsg.Message) (*Order, error) {
	text := nfkc(m.Text)
	o := &Order{Store: StoreRakuten, Currency: "JPY"}
	o.OrderNumber = findAfterLabel(text, reRakOrderNo)
	if d := findAfterLabel(text, reRakDate); d != "" {
		o.OrderDate = parseDate(d)
	}
	if o.OrderDate == nil && !m.OriginalDate.IsZero() {
		t := m.OriginalDate.In(jst)
		o.OrderDate = &t
	}
	if t := findAfterLabel(text, reRakTotal); t != "" {
		o.Total, _ = parseMoney(t)
	}

	ls := lines(text)
	for i, l := range ls {
		pm := reRakLine.FindStringSubmatch(l)
		if pm == nil {
			continue
		}
		item := LineItem{}
		item.UnitPrice, _ = strconv.ParseFloat(strings.ReplaceAll(pm[1], ",", ""), 64)
		item.Quantity, _ = strconv.ParseFloat(pm[2], 64)
		// Title may wrap across lines above the price line.
		var title []string
		for j := i - 1; j >= 0 && j >= i-4; j-- {
			if u := reRakURL.FindString(ls[j]); u != "" && item.URL == "" {
				item.URL = u
				continue
			}
			if reRakSkip.MatchString(ls[j]) {
				break
			}
			title = append([]string{ls[j]}, title...)
		}
		item.Name = strings.Join(title, " ")
		if item.Name == "" {
			continue
		}
		// Optional detail lines right after the price line.
		for j := i + 1; j < len(ls) && j <= i+3; j++ {
			if jm := reJAN.FindStringSubmatch(ls[j]); jm != nil {
				item.JAN = jm[1]
			}
		}
		item.Quantity = clampQty(item.Quantity)
		item.ModelNumber = modelFromTitle(item.Name)
		o.Items = append(o.Items, item)
	}
	if len(o.Items) == 0 {
		return o, ErrNoItems
	}
	return o, nil
}

package parsers

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/sysadminsmedia/homebox/backend/internal/core/services/x/orderimport/mailmsg"
)

// amazonJP parses Amazon.co.jp "ご注文の確認" order confirmations.
//
// Item blocks look like (after HTML flattening):
//
//	<item title>
//	https://www.amazon.co.jp/dp/B09Y2MYL5C
//	数量: 1
//	￥44,000
type amazonJP struct{}

var (
	reAmzOrderNo = regexp.MustCompile(`注文番号\s*[:：#]?\s*(\d{3}-\d{7}-\d{7})`)
	reAmzDate    = regexp.MustCompile(`(?:注文日|ご注文日)\s*[:：]?\s*(.+)`)
	reAmzTotal   = regexp.MustCompile(`(?:ご請求額|注文合計|合計金額|お支払い金額|合計)\s*(?:\(税込\))?\s*[:：]?\s*([¥￥]\s*[0-9,]+)`)
	reAmzQty     = regexp.MustCompile(`^数量\s*[:：]\s*(\d+)`)
	reAmzASIN    = regexp.MustCompile(`/(?:dp|gp/product)/([A-Z0-9]{10})`)
	reAmzPrice   = regexp.MustCompile(`^[¥￥]\s*[0-9,]+$`)
	reAmzSkip    = regexp.MustCompile(`^(?:https?://|数量|[¥￥]|販売:|販売者|出品者|コンディション|お届け予定|配送|ご注文|注文|小計|合計|配送料|$)`)
)

func (amazonJP) Store() string { return StoreAmazonJP }

func (amazonJP) Match(m *mailmsg.Message) bool {
	if senderDomainIs(m, "amazon.co.jp") {
		return true
	}
	return strings.Contains(m.Text, "Amazon.co.jp") && reAmzOrderNo.MatchString(nfkc(m.Text))
}

func (amazonJP) Parse(m *mailmsg.Message) (*Order, error) {
	text := nfkc(m.Text)
	o := &Order{Store: StoreAmazonJP, Currency: "JPY"}
	o.OrderNumber = findAfterLabel(text, reAmzOrderNo)
	if d := findAfterLabel(text, reAmzDate); d != "" {
		o.OrderDate = parseDate(d)
	}
	if o.OrderDate == nil && !m.OriginalDate.IsZero() {
		t := m.OriginalDate.In(jst)
		o.OrderDate = &t
	}
	if t := findAfterLabel(text, reAmzTotal); t != "" {
		o.Total, _ = parseMoney(t)
	}

	ls := lines(text)
	for i, l := range ls {
		qm := reAmzQty.FindStringSubmatch(l)
		if qm == nil {
			continue
		}
		item := LineItem{}
		item.Quantity, _ = strconv.ParseFloat(qm[1], 64)
		// Walk back to the title, collecting an ASIN link on the way.
		for j := i - 1; j >= 0 && j >= i-6; j-- {
			if a := reAmzASIN.FindStringSubmatch(ls[j]); a != nil && item.ASIN == "" {
				item.ASIN = a[1]
				item.URL = "https://www.amazon.co.jp/dp/" + a[1]
				continue
			}
			if reAmzSkip.MatchString(ls[j]) {
				if reAmzQty.MatchString(ls[j]) {
					break // previous item
				}
				continue
			}
			item.Name = ls[j]
			break
		}
		// Price: the first ¥ line within a few lines after (or before) 数量.
		for _, j := range []int{i + 1, i + 2, i + 3, i - 1, i - 2} {
			if j >= 0 && j < len(ls) && reAmzPrice.MatchString(ls[j]) {
				item.UnitPrice, _ = parseMoney(ls[j])
				break
			}
		}
		if item.Name == "" {
			continue
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

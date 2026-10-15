package parsers_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sysadminsmedia/homebox/backend/internal/core/services/x/orderimport/mailmsg"
	"github.com/sysadminsmedia/homebox/backend/internal/core/services/x/orderimport/parsers"
)

func load(t *testing.T, name string) *mailmsg.Message {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "testdata", name))
	require.NoError(t, err)
	m, err := mailmsg.Parse(raw)
	require.NoError(t, err)
	return m
}

func parse(t *testing.T, name string) *parsers.Order {
	t.Helper()
	m := load(t, name)
	p := parsers.Detect(m)
	require.NotNil(t, p, "no parser matched %s", name)
	o, err := p.Parse(m)
	require.NoError(t, err)
	return o
}

var amazonItems = []parsers.LineItem{
	{Name: "ソニー ワイヤレスノイズキャンセリングヘッドホン WH-1000XM5 ブラック", Quantity: 1, UnitPrice: 44000, ASIN: "B09Y2MYL5C", URL: "https://www.amazon.co.jp/dp/B09Y2MYL5C"},
	{Name: "Anker PowerCore 10000 (モバイルバッテリー 10000mAh)", Quantity: 2, UnitPrice: 2990, ASIN: "B019GNUT0C", URL: "https://www.amazon.co.jp/dp/B019GNUT0C"},
}

func TestAmazonDirectHTML(t *testing.T) {
	o := parse(t, "amazon_direct.eml")
	assert.Equal(t, parsers.StoreAmazonJP, o.Store)
	assert.Equal(t, "250-1234567-7654321", o.OrderNumber)
	require.NotNil(t, o.OrderDate)
	assert.Equal(t, "2026-10-10", o.OrderDate.Format("2006-01-02"))
	assert.InDelta(t, 49980, o.Total, 0.001)
	assert.Equal(t, amazonItems, o.Items)
}

func TestAmazonForwardedGmailMatchesDirect(t *testing.T) {
	m := load(t, "amazon_forwarded_gmail.eml")
	assert.True(t, m.Forwarded)
	assert.Equal(t, "auto-confirm@amazon.co.jp", m.OriginalFrom)
	assert.Equal(t, "jack@example.com", m.From)
	o := parse(t, "amazon_forwarded_gmail.eml")
	assert.Equal(t, "250-1234567-7654321", o.OrderNumber)
	assert.Equal(t, amazonItems, o.Items)
}

var rakutenItems = []parsers.LineItem{
	{Name: "【公式】象印 マイコン沸とう電動ポット 2.2L CD-WU22-WG", Quantity: 1, UnitPrice: 12800, JAN: "4974305213761", URL: "https://item.rakuten.co.jp/zojirushi/cd-wu22/"},
	{Name: "パナソニック 乾電池エボルタネオ 単3形 4本パック", Quantity: 3, UnitPrice: 498},
}

func TestRakutenISO2022JP(t *testing.T) {
	m := load(t, "rakuten_iso2022jp.eml")
	assert.Equal(t, "【楽天市場】注文内容ご確認（自動配信メール）", m.Subject)
	o := parse(t, "rakuten_iso2022jp.eml")
	assert.Equal(t, parsers.StoreRakuten, o.Store)
	assert.Equal(t, "123456-20261011-0012345678", o.OrderNumber)
	assert.Equal(t, "2026-10-11", o.OrderDate.Format("2006-01-02"))
	assert.InDelta(t, 14294, o.Total, 0.001)
	assert.Equal(t, rakutenItems, o.Items)
}

func TestRakutenForwardedAsAttachment(t *testing.T) {
	m := load(t, "rakuten_forwarded_attachment.eml")
	assert.True(t, m.Forwarded)
	assert.Equal(t, "<rakuten-0012345678@mail.rakuten.co.jp>", m.MessageID, "original Message-ID recovered")
	assert.Equal(t, "<outlook-fw-1@example.com>", m.OuterID)
	assert.Equal(t, "order@rakuten.co.jp", m.OriginalFrom)
	o := parse(t, "rakuten_forwarded_attachment.eml")
	assert.Equal(t, rakutenItems, o.Items)
}

func TestYodobashiBase64HTML(t *testing.T) {
	o := parse(t, "yodobashi_base64.eml")
	assert.Equal(t, parsers.StoreYodobashi, o.Store)
	assert.Equal(t, "1098765432", o.OrderNumber)
	assert.Equal(t, "2026-10-12", o.OrderDate.Format("2006-01-02"))
	assert.InDelta(t, 43460, o.Total, 0.001)
	assert.Equal(t, []parsers.LineItem{
		{Name: "Nintendo Switch(有機ELモデル) ホワイト [HEG-S-KAAAA]", Quantity: 1, UnitPrice: 37980, ModelNumber: "HEG-S-KAAAA", URL: "https://www.yodobashi.com/product/100000001006548029"},
		{Name: "ブラザー ラベルライター P-touch PT-P300BT", Quantity: 1, UnitPrice: 5480, ModelNumber: "PT-P300BT", JAN: "4977766791271"},
	}, o.Items)
}

func TestYodobashiForwardedOutlookJapaneseQuoted(t *testing.T) {
	m := load(t, "yodobashi_forwarded_outlook_ja.eml")
	assert.True(t, m.Forwarded)
	assert.Equal(t, "thanks_gochuumon@yodobashi.com", m.OriginalFrom)
	assert.Equal(t, "2026-10-13", m.OriginalDate.Format("2006-01-02"))
	o := parse(t, "yodobashi_forwarded_outlook_ja.eml")
	assert.Equal(t, "1098765555", o.OrderNumber)
	assert.Equal(t, []parsers.LineItem{
		{Name: "エレコム USB Type-Cケーブル 1.0m [MPA-CC10NBK]", Quantity: 2, UnitPrice: 1180, ModelNumber: "MPA-CC10NBK"},
	}, o.Items)
}

func TestUnknownStoreAndConfirmation(t *testing.T) {
	assert.Nil(t, parsers.Detect(load(t, "unknown_store.eml")))
	c := load(t, "gmail_forwarding_confirmation.eml")
	assert.Nil(t, parsers.Detect(c))
	assert.True(t, parsers.IsForwardingConfirmation(c))
	assert.False(t, parsers.IsForwardingConfirmation(load(t, "amazon_direct.eml")))
}

func TestInvalidEmail(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "not_an_email.eml"))
	require.NoError(t, err)
	_, err = mailmsg.Parse(raw)
	assert.Error(t, err)
}

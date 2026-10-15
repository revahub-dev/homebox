#!/usr/bin/env python3
"""Generates the handwritten .eml fixtures. Content is fictional; formats
approximate the stores' order mails and must be checked against real mail."""
import base64, quopri
from email.header import Header

def hdr(s, cs="utf-8"):
    return Header(s, cs).encode()

def write(name, data):
    with open(name, "wb") as f:
        f.write(data if isinstance(data, bytes) else data.encode("utf-8"))

AMZ_HTML = """<html><head><style>.x{color:red}</style><script>alert(1)</script></head><body>
<table><tr><td><h2>ご注文の確認</h2></td></tr>
<tr><td>注文番号： 250-1234567-7654321</td></tr>
<tr><td>注文日： 2026年10月10日</td></tr>
<tr><td><a href="https://www.amazon.co.jp/dp/B09Y2MYL5C/ref=pe_123">ソニー ワイヤレスノイズキャンセリングヘッドホン WH-1000XM5 ブラック</a></td></tr>
<tr><td>数量: 1</td></tr><tr><td>￥44,000</td></tr>
<tr><td><a href="https://www.amazon.co.jp/gp/product/B019GNUT0C?psc=1">Anker PowerCore 10000 (モバイルバッテリー 10000mAh)</a></td></tr>
<tr><td>数量: 2</td></tr><tr><td>￥２,９９０</td></tr>
<tr><td>ご請求額： ￥49,980</td></tr>
<tr><td><img src="https://tracking.example/pixel.gif"></td></tr>
</table></body></html>"""

AMZ_TEXT = """Amazon.co.jp をご利用いただき、ありがとうございます。
注文番号： 250-1234567-7654321
注文日： 2026年10月10日

ソニー ワイヤレスノイズキャンセリングヘッドホン WH-1000XM5 ブラック
https://www.amazon.co.jp/dp/B09Y2MYL5C
数量: 1
￥44,000

Anker PowerCore 10000 (モバイルバッテリー 10000mAh)
https://www.amazon.co.jp/dp/B019GNUT0C
数量: 2
￥2,990

ご請求額： ￥49,980
"""

qp_html = quopri.encodestring(AMZ_HTML.encode("utf-8")).decode("ascii")
write("amazon_direct.eml", f"""From: "Amazon.co.jp" <auto-confirm@amazon.co.jp>
To: jack@example.com
Subject: {hdr("Amazon.co.jpでのご注文 250-1234567-7654321")}
Date: Sat, 10 Oct 2026 10:00:00 +0900
Message-ID: <0100amz-order-7654321@email.amazonses.com>
MIME-Version: 1.0
Content-Type: multipart/alternative; boundary="b1"

--b1
Content-Type: text/plain; charset=utf-8
Content-Transfer-Encoding: base64

{base64.encodebytes(AMZ_TEXT.encode()).decode()}
--b1
Content-Type: text/html; charset=utf-8
Content-Transfer-Encoding: quoted-printable

{qp_html}
--b1--
""".replace("\n", "\r\n"))

# Same order, forwarded inline from Gmail (new outer Message-ID).
fwd_body = "これ登録しておいて\n\n---------- Forwarded message ---------\nFrom: Amazon.co.jp <auto-confirm@amazon.co.jp>\nDate: 2026年10月10日(土) 10:00\nSubject: Amazon.co.jpでのご注文 250-1234567-7654321\nTo: <jack@example.com>\n\n" + AMZ_TEXT
write("amazon_forwarded_gmail.eml", f"""From: Jack <jack@example.com>
To: orders-token@inbox.example.com
Subject: {hdr("Fwd: Amazon.co.jpでのご注文 250-1234567-7654321")}
Date: Sat, 10 Oct 2026 11:00:00 +0900
Message-ID: <CAJfwd-1@mail.gmail.com>
MIME-Version: 1.0
Content-Type: text/plain; charset=utf-8
Content-Transfer-Encoding: 8bit

{fwd_body}""")

RAK_TEXT = """このたびは楽天市場をご利用いただきありがとうございます。

[受注番号] 123456-20261011-0012345678
[日時] 2026-10-11 21:15:03
[注文者] 山田 太郎 様

[商品]
【公式】象印 マイコン沸とう電動ポット
2.2L CD-WU22-WG
https://item.rakuten.co.jp/zojirushi/cd-wu22/
価格  12,800(円) x  1(個) = 12,800(円)
JANコード：4974305213761

パナソニック 乾電池エボルタネオ 単3形 4本パック
価格  498(円) x  3(個) = 1,494(円)

**************************************************************
小計    14,294(円)
送料    0(円)
合計金額(税込)  14,294(円)
"""
rak_bytes = RAK_TEXT.encode("iso2022_jp")
write("rakuten_iso2022jp.eml", b"From: =?ISO-2022-JP?B?" + base64.b64encode("楽天市場".encode("iso2022_jp")) + b"?= <order@rakuten.co.jp>\r\n"
      b"To: jack@example.com\r\n"
      b"Subject: " + hdr("【楽天市場】注文内容ご確認（自動配信メール）", "iso-2022-jp").encode() + b"\r\n"
      b"Date: Sun, 11 Oct 2026 21:15:10 +0900\r\n"
      b"Message-ID: <rakuten-0012345678@mail.rakuten.co.jp>\r\n"
      b"MIME-Version: 1.0\r\n"
      b"Content-Type: text/plain; charset=ISO-2022-JP\r\n"
      b"Content-Transfer-Encoding: 7bit\r\n\r\n" + rak_bytes.replace(b"\n", b"\r\n"))

# Same Rakuten order forwarded as an attachment (Outlook style).
inner = open("rakuten_iso2022jp.eml", "rb").read()
write("rakuten_forwarded_attachment.eml", b"From: Jack <jack@example.com>\r\n"
      b"To: orders-token@inbox.example.com\r\n"
      b"Subject: FW: order\r\n"
      b"Date: Mon, 12 Oct 2026 08:00:00 +0900\r\n"
      b"Message-ID: <outlook-fw-1@example.com>\r\n"
      b"MIME-Version: 1.0\r\n"
      b"Content-Type: multipart/mixed; boundary=\"mx\"\r\n\r\n"
      b"--mx\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nSee attached.\r\n"
      b"--mx\r\nContent-Type: message/rfc822\r\nContent-Disposition: attachment; filename=\"order.eml\"\r\n\r\n"
      + inner + b"\r\n--mx--\r\n")

YDB_HTML = """<html><body><p>ヨドバシ・ドット・コムをご利用いただき、誠にありがとうございます。</p>
<p>【ご注文番号】 1098765432<br>【ご注文日】 2026年10月12日</p>
<p>・商品名：Nintendo Switch（有機ELモデル） ホワイト [HEG-S-KAAAA]<br>
数量：1<br>単価：37,980円<br>
<a href="https://www.yodobashi.com/product/100000001006548029/">商品ページ</a></p>
<p>・商品名：ブラザー ラベルライター P-touch PT-P300BT<br>
数量：1<br>単価：5,480円<br>メーカー品番：PT-P300BT<br>JANコード：4977766791271</p>
<p>【お支払い合計】 43,460円</p></body></html>"""
write("yodobashi_base64.eml", f"""From: =?UTF-8?B?{base64.b64encode("ヨドバシ・ドット・コム".encode()).decode()}?= <thanks_gochuumon@yodobashi.com>
To: jack@example.com
Subject: {hdr("ヨドバシ・ドット・コム：ご注文ありがとうございます")}
Date: Mon, 12 Oct 2026 09:30:00 +0900
Message-ID: <ydb-1098765432@yodobashi.com>
MIME-Version: 1.0
Content-Type: text/html; charset=UTF-8
Content-Transfer-Encoding: base64

{base64.encodebytes(YDB_HTML.encode()).decode()}""".replace("\n", "\r\n"))

YDB_TEXT = """【ご注文番号】 1098765555
【ご注文日】 2026年10月13日
・商品名：エレコム USB Type-Cケーブル 1.0m [MPA-CC10NBK]
数量：2
単価：1,180円
【お支払い合計】 2,360円"""
quoted = "\n".join("> " + l for l in YDB_TEXT.split("\n"))
write("yodobashi_forwarded_outlook_ja.eml", f"""From: Jack <jack@example.com>
To: orders-token@inbox.example.com
Subject: {hdr("転送: ご注文ありがとうございます")}
Date: Tue, 13 Oct 2026 12:00:00 +0900
Message-ID: <outlook-ja-2@example.com>
MIME-Version: 1.0
Content-Type: text/plain; charset=utf-8

よろしく

-------- 転送メッセージ --------
差出人: ヨドバシ・ドット・コム <thanks_gochuumon@yodobashi.com>
送信日時: 2026年10月13日 10:05
件名: ご注文ありがとうございます

{quoted}
""")

write("gmail_forwarding_confirmation.eml", """From: Gmail Team <forwarding-noreply@google.com>
To: orders-token@inbox.example.com
Subject: (#123456789) Gmail Forwarding Confirmation - Receive Mail from jack@example.com
Date: Wed, 14 Oct 2026 08:00:00 +0000
Message-ID: <gmail-confirm-1@google.com>
MIME-Version: 1.0
Content-Type: text/plain; charset=utf-8

jack@example.com has requested to automatically forward mail to your email address.
Confirmation code: 123456789
To allow jack@example.com to automatically forward mail to your address, please click the link below to confirm the request:
https://mail-settings.google.com/mail/vf-example
""")

write("unknown_store.eml", """From: Example Shop <shop@example-shop.jp>
To: jack@example.com
Subject: =?UTF-8?B?""" + base64.b64encode("ご注文ありがとうございます".encode()).decode() + """?=
Date: Wed, 14 Oct 2026 10:00:00 +0900
Message-ID: <shop-1@example-shop.jp>
MIME-Version: 1.0
Content-Type: text/plain; charset=utf-8

注文番号 A-0001
・無印良品 アクリル仕切りスタンド 3仕切り ×1 1,290円
合計 1,290円
""")

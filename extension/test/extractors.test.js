import { describe, it, expect, beforeAll } from "vitest";
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { JSDOM } from "jsdom";

const require = createRequire(import.meta.url);
let amazon, rakuten, yodobashi, summarize;

beforeAll(() => {
  require("../src/sites/common.js");
  amazon = require("../src/sites/amazon.js");
  rakuten = require("../src/sites/rakuten.js");
  yodobashi = require("../src/sites/yodobashi.js");
  ({ summarize } = require("../src/content.js"));
});

const load = (name, url) => {
  const dom = new JSDOM(readFileSync(new URL(`./fixtures/${name}`, import.meta.url), "utf8"), { url });
  return [dom.window.document, new URL(url)];
};

describe("amazon", () => {
  it("matches only product URLs", () => {
    expect(amazon.matches(new URL("https://www.amazon.co.jp/Sony-WH-1000XM5/dp/B09Y2MYL5C/ref=sr_1_1"))).toBe(true);
    expect(amazon.matches(new URL("https://www.amazon.co.jp/gp/product/B019GNUT0C"))).toBe(true);
    expect(amazon.matches(new URL("https://www.amazon.co.jp/s?k=headphones"))).toBe(false);
    expect(amazon.matches(new URL("https://www.amazon.com/dp/B09Y2MYL5C"))).toBe(false);
  });

  it("extracts from the tech-spec table layout", () => {
    const [doc, url] = load("amazon-techspec.html", "https://www.amazon.co.jp/Sony/dp/B09Y2MYL5C");
    expect(amazon.extract(doc, url)).toEqual({
      name: "ソニー ワイヤレスノイズキャンセリングヘッドホン WH-1000XM5 ブラック",
      asin: "B09Y2MYL5C",
      jan: "",
      modelNumber: "WH-1000XM5 B",
    });
  });

  it("extracts from the detail-bullets layout (with RTL marks) and gp/product URL", () => {
    const [doc, url] = load("amazon-bullets.html", "https://www.amazon.co.jp/gp/product/B019GNUT0C?th=1");
    expect(amazon.extract(doc, url)).toEqual({
      name: "Anker PowerCore 10000 (モバイルバッテリー 10000mAh)",
      asin: "B019GNUT0C",
      jan: "0848061074321",
      modelNumber: "A1263",
    });
  });
});

describe("rakuten", () => {
  it("matches item pages only", () => {
    expect(rakuten.matches(new URL("https://item.rakuten.co.jp/shop/item-123/"))).toBe(true);
    expect(rakuten.matches(new URL("https://item.rakuten.co.jp/shop/"))).toBe(false);
    expect(rakuten.matches(new URL("https://search.rakuten.co.jp/search/mall/pot/"))).toBe(false);
  });

  it("extracts JAN (full-width) and model from description text", () => {
    const [doc, url] = load("rakuten.html", "https://item.rakuten.co.jp/zojirushi/cd-wu22/");
    expect(rakuten.extract(doc, url)).toEqual({
      name: "【公式】象印 マイコン沸とう電動ポット ２．２Ｌ CD-WU22-WG",
      asin: "",
      jan: "4974305213761",
      modelNumber: "CD-WU22-WG",
    });
  });

  it("extracts from a spec table", () => {
    const [doc, url] = load("rakuten-table.html", "https://item.rakuten.co.jp/shop/lr6/");
    expect(rakuten.extract(doc, url)).toMatchObject({ jan: "4549980490712", modelNumber: "LR6NJ/4H" });
  });
});

describe("yodobashi", () => {
  it("matches product pages only", () => {
    expect(yodobashi.matches(new URL("https://www.yodobashi.com/product/100000001006548029/"))).toBe(true);
    expect(yodobashi.matches(new URL("https://www.yodobashi.com/category/"))).toBe(false);
  });

  it("extracts from the spec table", () => {
    const [doc, url] = load("yodobashi.html", "https://www.yodobashi.com/product/100000001006548029/");
    expect(yodobashi.extract(doc, url)).toEqual({
      name: "Nintendo Switch（有機ELモデル） ホワイト [HEG-S-KAAAA]",
      asin: "",
      jan: "4902370548495",
      modelNumber: "HEG-S-KAAAA",
    });
  });

  it("falls back to text patterns", () => {
    const [doc, url] = load("yodobashi-text.html", "https://www.yodobashi.com/product/100000001004791234/");
    expect(yodobashi.extract(doc, url)).toEqual({
      name: "ブラザー ラベルライター P-touch PT-P300BT",
      asin: "",
      jan: "4977766791271",
      modelNumber: "PT-P300BT",
    });
  });
});

describe("summarize", () => {
  it("returns null for no matches", () => {
    expect(summarize([])).toBeNull();
    expect(summarize(undefined)).toBeNull();
  });
  it("prefers the strongest tier", () => {
    const s = summarize([
      { id: "a", confidence: "similar" },
      { id: "b", confidence: "exact", locationPath: "Garage > Drawer 3" },
      { id: "c", confidence: "exact" },
    ]);
    expect(s).toMatchObject({ tier: "exact", count: 2, top: { id: "b" } });
    expect(summarize([{ id: "x", confidence: "similar" }, { id: "y", confidence: "similar" }])).toMatchObject({ tier: "similar", count: 2 });
  });
});

describe("locales", () => {
  it("en and ja define the same keys", () => {
    const en = JSON.parse(readFileSync(new URL("../_locales/en/messages.json", import.meta.url)));
    const ja = JSON.parse(readFileSync(new URL("../_locales/ja/messages.json", import.meta.url)));
    expect(Object.keys(ja).sort()).toEqual(Object.keys(en).sort());
  });
});

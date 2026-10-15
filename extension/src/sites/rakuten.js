// Rakuten Ichiba item pages (item.rakuten.co.jp/<shop>/<item>/). Shops control
// most of the page, so JAN/model are found by label or text pattern.
(function (root) {
  const HBX = (root.HBX = root.HBX || { sites: {} });
  const u = () => HBX.util;

  const SELECTORS = {
    title: [".normal_reserve_item_name", "span.item_name", "h1.item_name", "meta[property='og:title']", "h1"],
    janLabels: ["JAN", "JANコード", "JAN CODE", "JANコード/ISBNコード", "EAN"],
    modelLabels: ["型番", "メーカー型番", "品番", "メーカー品番", "モデル番号"],
    janPattern: /JAN(?:コード| ?CODE)?\s*[:：]?\s*(\d{13}|\d{8})(?!\d)/i,
    modelPattern: /(?:メーカー)?型番\s*[:：]\s*([A-Za-z0-9][A-Za-z0-9\-_/.]{1,40})/,
  };

  const site = {
    id: "rakuten",
    SELECTORS,
    matches(url) {
      return url.hostname === "item.rakuten.co.jp" && url.pathname.split("/").filter(Boolean).length >= 2;
    },
    extract(doc) {
      const jan = u().normalizeJAN(u().labeledValue(doc, SELECTORS.janLabels)) ||
        u().normalizeJAN(u().textPattern(doc, SELECTORS.janPattern));
      const model = u().labeledValue(doc, SELECTORS.modelLabels) || u().textPattern(doc, SELECTORS.modelPattern);
      return { name: u().text(doc, SELECTORS.title), asin: "", jan, modelNumber: model };
    },
  };

  HBX.sites.rakuten = site;
  if (typeof module !== "undefined") module.exports = site;
})(globalThis);

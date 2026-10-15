// Yodobashi.com product pages (www.yodobashi.com/product/<id>/).
(function (root) {
  const HBX = (root.HBX = root.HBX || { sites: {} });
  const u = () => HBX.util;

  const SELECTORS = {
    title: ["#products_maintitle span[itemprop='name']", "#products_maintitle", "h1[itemprop='name']", "meta[property='og:title']", "h1"],
    janLabels: ["JANコード", "JAN", "JAN/EAN"],
    modelLabels: ["メーカー品番", "型番", "品番", "モデル番号"],
    janPattern: /JAN(?:コード)?\s*[:：]?\s*(\d{13}|\d{8})(?!\d)/,
    modelPattern: /メーカー品番\s*[:：]?\s*([A-Za-z0-9][A-Za-z0-9\-_/.]{1,40})/,
  };

  const site = {
    id: "yodobashi",
    SELECTORS,
    matches(url) {
      return /(^|\.)yodobashi\.com$/.test(url.hostname) && /^\/product\/\d+/.test(url.pathname);
    },
    extract(doc) {
      const jan = u().normalizeJAN(u().labeledValue(doc, SELECTORS.janLabels)) ||
        u().normalizeJAN(u().textPattern(doc, SELECTORS.janPattern));
      const model = u().labeledValue(doc, SELECTORS.modelLabels) || u().textPattern(doc, SELECTORS.modelPattern);
      return { name: u().text(doc, SELECTORS.title), asin: "", jan, modelNumber: model };
    },
  };

  HBX.sites.yodobashi = site;
  if (typeof module !== "undefined") module.exports = site;
})(globalThis);

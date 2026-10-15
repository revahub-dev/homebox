// Amazon.co.jp product pages. Update selectors here when Amazon changes markup.
(function (root) {
  const HBX = (root.HBX = root.HBX || { sites: {} });
  const u = () => HBX.util;

  const SELECTORS = {
    title: ["#productTitle", "#title", "meta[name='title']"],
    asinInput: ["input#ASIN", "input[name='ASIN']", "[data-asin]:not([data-asin=''])"],
    modelLabels: ["型番", "メーカー型番", "モデル番号", "Item model number", "Model Number", "Model number"],
    janLabels: ["JAN", "JANコード", "EAN", "UPC"],
    asinLabels: ["ASIN"],
  };

  const site = {
    id: "amazon",
    SELECTORS,
    matches(url) {
      return /(^|\.)amazon\.co\.jp$/.test(url.hostname) && /\/(dp|gp\/product)\/[A-Z0-9]{10}/i.test(url.pathname);
    },
    extract(doc, url) {
      const m = url.pathname.match(/\/(?:dp|gp\/product)\/([A-Z0-9]{10})/i);
      let asin = m ? m[1].toUpperCase() : "";
      if (!asin) {
        for (const sel of SELECTORS.asinInput) {
          const el = doc.querySelector(sel);
          const v = el && (el.value || el.getAttribute("value") || el.getAttribute("data-asin"));
          if (v) { asin = v.trim().toUpperCase(); break; }
        }
      }
      if (!asin) asin = u().labeledValue(doc, SELECTORS.asinLabels).toUpperCase();
      return {
        name: u().text(doc, SELECTORS.title),
        asin,
        jan: u().normalizeJAN(u().labeledValue(doc, SELECTORS.janLabels)),
        modelNumber: u().labeledValue(doc, SELECTORS.modelLabels),
      };
    },
  };

  HBX.sites.amazon = site;
  if (typeof module !== "undefined") module.exports = site;
})(globalThis);

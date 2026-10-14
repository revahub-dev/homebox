// Shared DOM helpers for site extractors. Loaded as a classic content script
// (attaches to globalThis.HBX) and as a CommonJS module in tests.
(function (root) {
  const HBX = (root.HBX = root.HBX || { sites: {} });

  const clean = (s) =>
    (s || "").replace(/[\u200e\u200f\u202a-\u202e]/g, "").replace(/[\s\u3000]+/g, " ").trim();

  // Full-width digits/letters -> ASCII, for codes like "４９０１…".
  const toHalfWidth = (s) =>
    (s || "").replace(/[\uFF01-\uFF5E]/g, (c) => String.fromCharCode(c.charCodeAt(0) - 0xfee0));

  function text(doc, selectors) {
    for (const sel of selectors) {
      const el = doc.querySelector(sel);
      if (!el) continue;
      const v = clean(el.getAttribute("content") ?? el.textContent);
      if (v) return v;
    }
    return "";
  }

  function labelMatches(label, labels) {
    const l = clean(label).replace(/[:：]/g, "").trim().toLowerCase();
    return labels.some((x) => l === x.toLowerCase());
  }

  // Finds a value next to a label in th/td tables, dt/dd lists and
  // "Label : Value" list items. labels are compared case-insensitively.
  function labeledValue(doc, labels) {
    for (const row of doc.querySelectorAll("tr")) {
      const cells = row.querySelectorAll("th, td");
      if (cells.length >= 2 && labelMatches(cells[0].textContent, labels)) return clean(cells[1].textContent);
    }
    for (const dt of doc.querySelectorAll("dt")) {
      const dd = dt.nextElementSibling;
      if (dd && dd.tagName === "DD" && labelMatches(dt.textContent, labels)) return clean(dd.textContent);
    }
    for (const li of doc.querySelectorAll("li")) {
      const spans = li.querySelectorAll("span");
      if (spans.length >= 2) {
        const [a, b] = [spans[spans.length - 2], spans[spans.length - 1]];
        if (labelMatches(a.textContent, labels)) return clean(b.textContent);
      }
    }
    return "";
  }

  // Regex over visible body text, e.g. "JANコード：4901234567894".
  function textPattern(doc, re) {
    const body = toHalfWidth(doc.body ? doc.body.textContent : "");
    const m = body.match(re);
    return m ? clean(m[1]) : "";
  }

  function normalizeJAN(s) {
    const d = toHalfWidth(s).replace(/\D/g, "");
    return d.length === 8 || d.length === 13 ? d : "";
  }

  HBX.util = { clean, toHalfWidth, text, labeledValue, textPattern, normalizeJAN };
  if (typeof module !== "undefined") module.exports = HBX.util;
})(globalThis);

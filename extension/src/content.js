// Content script: extract product identity, ask the background worker for
// matches, and render a small dismissible badge. Silent on any failure.
(function (root) {
  const HBX = (root.HBX = root.HBX || { sites: {} });

  // Pure: picks the headline for a match list. Exported for tests.
  function summarize(matches) {
    if (!Array.isArray(matches) || matches.length === 0) return null;
    for (const tier of ["exact", "likely", "similar"]) {
      const ms = matches.filter((m) => m.confidence === tier);
      if (ms.length) return { tier, count: ms.length, top: ms[0] };
    }
    return null;
  }
  HBX.summarize = summarize;
  if (typeof module !== "undefined") {
    module.exports = { summarize };
    return;
  }
  if (typeof chrome === "undefined" || !chrome.runtime?.id) return;

  const url = new URL(location.href);
  const site = Object.values(HBX.sites).find((s) => s.matches(url));
  if (!site) return;

  const dismissKey = "hbx-dismissed:" + url.origin + url.pathname;
  try { if (sessionStorage.getItem(dismissKey)) return; } catch { /* ignore */ }

  let product;
  try { product = site.extract(document, url); } catch { return; }
  if (!product || !(product.name || product.asin || product.jan || product.modelNumber)) return;

  chrome.runtime.sendMessage({ type: "dupcheck", payload: product }, (res) => {
    if (chrome.runtime.lastError || !res?.ok) return; // fail silently
    const s = summarize(res.data?.matches);
    if (s) render(s, res.serverUrl);
  });

  function render(s, serverUrl) {
    const t = (k, sub) => chrome.i18n.getMessage(k, sub) || k;
    const host = document.createElement("div");
    host.style.cssText = "position:fixed;right:16px;bottom:16px;z-index:2147483647;";
    const shadow = host.attachShadow({ mode: "closed" });
    const colors = { exact: "#b42318", likely: "#b54708", similar: "#475467" };
    const style = document.createElement("style");
    style.textContent = `
      .b{font:13px/1.4 system-ui,sans-serif;background:#fff;color:#101828;border-left:4px solid ${colors[s.tier]};
         box-shadow:0 4px 12px rgba(0,0,0,.18);border-radius:6px;padding:8px 32px 8px 12px;max-width:340px;position:relative}
      .h{font-weight:600;color:${colors[s.tier]}} a{color:#175cd3;text-decoration:none} a:hover{text-decoration:underline}
      button{position:absolute;top:4px;right:6px;border:0;background:none;font-size:16px;cursor:pointer;color:#667085}`;
    const box = document.createElement("div");
    box.className = "b";
    const key = { exact: "badgeExact", likely: "badgeLikely", similar: "badgeSimilar" }[s.tier];
    const head = document.createElement("div");
    head.className = "h";
    head.textContent = t(key, [String(s.count)]) + (s.top.locationPath ? " · " + s.top.locationPath : "");
    const link = document.createElement("a");
    link.href = serverUrl + "/item/" + encodeURIComponent(s.top.id);
    link.target = "_blank";
    link.rel = "noopener noreferrer";
    link.textContent = s.top.name + " — " + t("badgeOpen");
    const close = document.createElement("button");
    close.textContent = "×";
    close.title = t("badgeDismiss");
    close.setAttribute("aria-label", t("badgeDismiss"));
    close.addEventListener("click", () => {
      host.remove();
      try { sessionStorage.setItem(dismissKey, "1"); } catch { /* ignore */ }
    });
    box.append(head, link, close);
    shadow.append(style, box);
    document.documentElement.appendChild(host);
  }
})(globalThis);

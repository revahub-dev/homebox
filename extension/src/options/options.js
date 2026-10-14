const t = (k, sub) => chrome.i18n.getMessage(k, sub) || k;
const $ = (id) => document.getElementById(id);
const status = (msg) => ($("status").textContent = msg);

for (const el of document.querySelectorAll("[data-i18n]")) el.textContent = t(el.dataset.i18n);

chrome.storage.local.get(["serverUrl", "apiKey"]).then(({ serverUrl = "", apiKey = "" }) => {
  $("serverUrl").value = serverUrl;
  $("apiKey").value = apiKey;
});

function parseOrigin(raw) {
  try {
    const u = new URL(raw.trim());
    if (u.protocol !== "https:" && u.protocol !== "http:") return null;
    return { base: (u.origin + u.pathname).replace(/\/+$/, ""), origin: u.origin };
  } catch {
    return null;
  }
}

$("save").addEventListener("click", () => {
  const parsed = parseOrigin($("serverUrl").value);
  if (!parsed) return status(t("optionsInvalidUrl"));
  // permissions.request must run directly in the user gesture, before any await.
  chrome.permissions.request({ origins: [parsed.origin + "/*"] }, async (granted) => {
    if (!granted) return status(t("optionsPermissionDenied"));
    const { serverUrl: old } = await chrome.storage.local.get("serverUrl");
    await chrome.storage.local.set({ serverUrl: parsed.base, apiKey: $("apiKey").value.trim() });
    // Drop access to a previously configured server.
    const oldOrigin = old && parseOrigin(old)?.origin;
    if (oldOrigin && oldOrigin !== parsed.origin) chrome.permissions.remove({ origins: [oldOrigin + "/*"] });
    status(t("optionsSaved"));
  });
});

$("test").addEventListener("click", () => {
  status("…");
  chrome.runtime.sendMessage({ type: "test" }, (res) => {
    if (res?.ok) return status(t("optionsTestOk", [res.data?.item?.name || res.data?.name || "?"]));
    if (res?.reason === "unauthorized") return status(t("optionsTestUnauthorized"));
    if (res?.reason === "not_configured" || res?.reason === "no_permission") return status(t("optionsNotConfigured"));
    status(t("optionsTestFailed"));
  });
});

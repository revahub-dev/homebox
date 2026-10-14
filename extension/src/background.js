// Service worker: holds the API key and is the only place that talks to the
// Homebox server. Content scripts never see the key.
const TIMEOUT_MS = 1200; // leaves headroom inside the 1.5 s budget

async function getConfig() {
  const { serverUrl = "", apiKey = "" } = await chrome.storage.local.get(["serverUrl", "apiKey"]);
  return { serverUrl: serverUrl.replace(/\/+$/, ""), apiKey };
}

async function hasPermission(serverUrl) {
  try {
    return await chrome.permissions.contains({ origins: [new URL(serverUrl).origin + "/*"] });
  } catch {
    return false;
  }
}

async function call(path, init = {}) {
  const { serverUrl, apiKey } = await getConfig();
  if (!serverUrl || !apiKey) return { ok: false, reason: "not_configured" };
  if (!(await hasPermission(serverUrl))) return { ok: false, reason: "no_permission" };

  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), init.timeout ?? TIMEOUT_MS);
  try {
    const res = await fetch(serverUrl + path, {
      ...init,
      signal: ctrl.signal,
      credentials: "omit",
      headers: { "Content-Type": "application/json", Authorization: "Bearer " + apiKey, ...(init.headers || {}) },
    });
    if (!res.ok) return { ok: false, reason: res.status === 401 ? "unauthorized" : "http_" + res.status };
    return { ok: true, serverUrl, data: await res.json() };
  } catch {
    return { ok: false, reason: "unreachable" };
  } finally {
    clearTimeout(timer);
  }
}

chrome.runtime.onMessage.addListener((msg, _sender, sendResponse) => {
  if (msg?.type === "dupcheck") {
    const p = msg.payload || {};
    const body = JSON.stringify({
      name: String(p.name || "").slice(0, 500),
      asin: String(p.asin || "").slice(0, 64),
      jan: String(p.jan || "").slice(0, 64),
      modelNumber: String(p.modelNumber || "").slice(0, 255),
      limit: 10,
    });
    call("/api/v1/duplicates/check", { method: "POST", body }).then(sendResponse);
    return true;
  }
  if (msg?.type === "test") {
    call("/api/v1/users/self", { method: "GET", timeout: 5000 }).then(sendResponse);
    return true;
  }
  return false;
});

chrome.action.onClicked.addListener(() => chrome.runtime.openOptionsPage());

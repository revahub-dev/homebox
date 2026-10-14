# Homebox Duplicate Check (Chrome extension, MV3)

Shows a small badge on product pages when you already own the item in your Homebox:

> **You own 1 (exact) · Garage > Drawer 3**
> Sony WH-1000XM5 — Open in Homebox

Supported pages: amazon.co.jp (`/dp/…`, `/gp/product/…`), item.rakuten.co.jp, and yodobashi.com (`/product/…`).
Nothing is shown when there's no match or the server can't be reached. UI languages: English and Japanese (follows the browser language).

Needs a Homebox server with the `/api/v1/duplicates/check` endpoint (PR #1 on this fork).

## Install (unpacked)
1. In Homebox, open **Profile → API keys**, create a key and copy it.
2. In Chrome, go to `chrome://extensions`, turn on **Developer mode** and click **Load unpacked**. Pick this `extension/` folder (the one containing `manifest.json`).
3. The options page opens when you click the toolbar icon (or use *Details → Extension options*). Enter the server URL (e.g. `https://homebox.example.com`) and the API key, then click **Save**. Chrome asks for permission to access that one origin; allow it.
4. Click **Test connection**. It should say "Connected as <your name>".
5. Open a product page, e.g. `https://www.amazon.co.jp/dp/<ASIN>`.

## How it works
- `src/sites/<site>.js`: one module per store. **Selectors and labels live at the top in `SELECTORS`**, so that's where to fix things when a store changes its markup. `src/sites/common.js` has the shared helpers (label/value lookup in tables, `dt/dd` and bullet lists, text patterns, full-width → half-width).
- `src/content.js` extracts `{name, asin, jan, modelNumber}` and sends it to the service worker. It renders the badge inside a closed shadow root. Dismissing it hides the badge for that page for the rest of the tab session.
- `src/background.js` is the only code that holds the API key. It calls `POST /api/v1/duplicates/check` with `Authorization: Bearer <key>` (Homebox API keys are only accepted in that header), with a 1.2 s timeout.
- Permissions: only `storage`. Host access is requested at runtime for the configured server origin only (`optional_host_permissions`). Access to a previously saved origin is removed when the server changes.
- No server CORS change is needed: requests come from the service worker, which isn't subject to CORS once host permission is granted.
- Matching identifiers: Homebox has no ASIN/JAN columns, so add custom fields named `ASIN` or `JAN` to items (the model number uses the built-in field or a `型番` custom field). See PR #1.

## Tests
```
cd extension
npm install
npm test
```
The extractor tests run against hand-made HTML fixtures in `test/fixtures/` that reproduce the stores' markup patterns. They aren't saved copies of live pages.

License: AGPL-3.0, same as the repository.

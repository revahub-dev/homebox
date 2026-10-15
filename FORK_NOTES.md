# Fork notes (revahub-dev/homebox)

Fork additions live in `x_`-prefixed files and `x/` packages to keep upstream merges cheap.
The only edits to upstream files are these hook points:

| Hook point | Change |
|---|---|
| `backend/app/api/routes.go` | registers `/duplicates/check`, `/import/*`, `/idle/*`, `/build/*` and `/capture/*` routes (the crop route sits in the attachment-token group) |
| `backend/app/api/recurring.go` | hourly `x-idle-digest` task (each collection gets at most one digest per interval); `x-capture-worker` (crash recovery and retention for the photo queue) |
| `backend/app/api/app.go` | `captureService()`: the single `capture.Service` shared by the handlers and the worker |
| `backend/app/api/handlers/v1/controller.go` | `WithCapture` option and a `capture` field on `V1Controller` |
| `backend/app/api/main.go` | body-size override for `/api/v1/import/eml` (50 × 10 MB) |
| `backend/internal/data/repo/repos_all.go` | adds `XOrderImport`, `XUseBuild` and `XCapture` repositories |
| `backend/internal/sys/config/conf.go` | `OrderImportConf` (`HBOX_ORDER_IMPORT_*`), `AIConf` (`HBOX_AI_*`, now with vision model, response format and timeout), `IdleConf` (`HBOX_IDLE_*`), `CaptureConf` (`HBOX_CAPTURE_*`) |
| `backend/go.mod` | `golang.org/x/net` promoted to a direct dependency |
| `frontend/lib/api/user.ts` | `xImport`, `xUse` and `xCapture` API clients |
| `frontend/layouts/default.vue` | nav entries "Snap" (id 89), "Order inbox" (90), "Idle items" (91), "Build" (92); "Snap to add" is the primary create button, the old Create menu is now "Enter manually"; a phone-only floating camera button |
| `frontend/components/Entity/CreateModal.vue` | a "snap a photo instead" hint above the manual item form |
| `frontend/components/Location/Selector.vue` | optional `label` prop |
| `frontend/pages/tag/[id].vue`, `frontend/pages/location/[id]/index/index.vue` | "What can I build?" button (`components/X/BuildButton.vue`) |
| `frontend/pages/item/[id]/index.vue` | "Mark used" button (`components/X/MarkUsedButton.vue`) |
| `frontend/locales/{en,ja-JP}.json` | `menu.order_inbox`, `menu.idle_items`, `menu.build`, `menu.snap`, `menu.snap_to_add`, `menu.add_manually` and the `x_import`, `x_idle`, `x_build`, `x_snap` namespaces |
| generated: ent code, swagger docs, `data-contracts.ts` | regenerated via `task --force generate` |

Fork tables (`x_inbound_addresses`, `x_inbound_messages`, `x_import_candidates`, `x_entity_activities`,
`x_idle_digests`, `x_projects`, `x_project_parts`, `x_capture_sessions`, `x_capture_photos`, `x_capture_items`) reference
`groups` / `entities` through FKs declared only in the SQL migrations, so upstream `group.go` and
`entity.go` ent schemas are untouched.

## Order import configuration

- `HBOX_ORDER_IMPORT_INBOUND_DOMAIN`: domain for `orders-<token>@<domain>` forwarding addresses (optional).
- `HBOX_ORDER_IMPORT_LLM_BASE_URL`, `_LLM_API_KEY`, `_LLM_MODEL`, `_LLM_VISION_MODEL`, `_LLM_TIMEOUT_SEC`:
  OpenAI-compatible fallback extractor. Disabled when the base URL or model is unset.
- Webhook: `POST /api/v1/import/inbound/{token}` with raw MIME (≤10 MB), unauthenticated, token-gated.

## Use & build configuration

- `HBOX_AI_BASE_URL`, `HBOX_AI_API_KEY`, `HBOX_AI_MODEL`, `HBOX_AI_TIMEOUT_SEC` (default 120), plus `HBOX_AI_VISION_MODEL` and `HBOX_AI_RESPONSE_FORMAT` (see the README): the shared
  OpenAI-compatible endpoint for "What can I build?" and sale-listing drafts. When unset, the
  `HBOX_ORDER_IMPORT_LLM_*` endpoint is reused. With neither, `/build/suggest` returns 503 and sale
  drafts fall back to a template.
- `HBOX_IDLE_ENABLED` (true), `HBOX_IDLE_DAYS` (180), `HBOX_IDLE_DIGEST_LIMIT` (10),
  `HBOX_IDLE_SNOOZE_DAYS` (90), `HBOX_IDLE_INTERVAL_DAYS` (7). The digest goes through the collection's
  active notifiers; set `HBOX_OPTIONS_HOSTNAME` to include a link to the Idle items page.
- "Last touched" is the latest of created, edited (`entities.updated_at`) and "Mark used". Views are not tracked.

## Photo-first capture (Snap)

Code: `backend/internal/core/services/x/{vision,capture}`, `backend/internal/core/services/x/aichat` (shared client),
`backend/internal/data/repo/repo_x_capture.go`, `frontend/pages/x/snap.vue`, `frontend/components/X/Snap/`.

- **Flow:** `POST /capture/sessions` (mode + location) → `POST /capture/sessions/{id}/photos` (multipart, answers 202) →
  client polls `GET /capture/sessions/{id}` → per item `accept | merge | ignore | undo`, `PATCH` to edit, and
  `POST /capture/sessions/{id}/accept` for "accept all ready". `GET /capture/status` reports provider, model, local/cloud
  and queue depth (never the key). 503 means no AI endpoint is configured; 415/413/429 are bad file, too large, daily cap.
- **Queue:** database-backed (`x_capture_photos`), no new infrastructure. Workers claim the oldest queued photo with an
  atomic `UPDATE ... WHERE status='queued'`; photos stuck in `running` for longer than `max(5 min, 2 × timeout)` are
  re-queued, and fail after 3 attempts. Image bytes are kept only until review (`HBOX_CAPTURE_RETENTION_DAYS`).
- **Provider seam:** `vision.Provider` (`Detect`, `Name`). `vision.NewOpenAI` wraps `aichat.VisionChatter`, so cloud and
  local are the same code with different env vars. Model output is untrusted: `vision.ParseReply` caps the number of
  detections, removes overlapping duplicates, rejects absurd boxes, clamps quantities and calibrates confidence
  (shelf photos without a box and generic names are discounted).
- **Confirm grid buckets:** *ready* ≥ 0.80, *review* 0.50–0.80, *maybe* < 0.50. A detection with an exact (identifier) or
  identical-name match in the inventory is never in "accept all ready"; its card shows a banner with a one-tap **Merge**
  (adds the quantity to the existing item). Duplicates across photos of one session are folded into one card (larger count wins).
- **Undo** works for 30 minutes: accept deletes the created item, merge subtracts the quantity and removes the added
  photo, ignore re-opens the card.
- New items go to the chosen location, else to a location named "Unsorted" (created on first use). Categories
  are matched to an existing tag by name; tags are never created automatically.
- Items accepted from a photo get the crop as their primary photo (through the normal attachment pipeline, so thumbnails work).
- Manual item forms are unchanged. The item editor already hides advanced sections unless "advanced view" is on.
- Not done yet (later phases of the photo-first plan): barcode/label OCR passes, receipt-photo routing into the order inbox,
  per-collection AI settings in the UI, background re-analysis with a bigger model.

## MVP status

| PR | Branch | Contents | Based on |
|---|---|---|---|
| #1 | `feat/duplicate-check` | `GET/POST /api/v1/duplicates/check` | `main` |
| #2 | `feat/chrome-extension` | MV3 extension in `extension/` (Amazon JP, Rakuten, Yodobashi) | #1 |
| #3 | `feat/order-import` | forwarding webhook, `.eml`/photo upload, store parsers, LLM fallback, review inbox | #1 |
| #4 | `feat/use-and-build` | idle-item digest + Idle items page, "What can I build?", saved projects with reserved parts | #3 |
| #5 | `feat/photo-first` | Snap: camera burst + gallery upload, vision provider, DB queue, confirm grid, dup banner + Merge, Snap as primary add | #4 |

**Merge order:** #1 → #3 → #4 → #5, then #2 (#2 only depends on #1 and can go in any time after it).
Rebase nothing; after each merge, retarget the next PR's base to `main` in the GitHub UI so its diff
shrinks to its own commits.

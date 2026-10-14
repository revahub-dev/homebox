# Fork notes (revahub-dev/homebox)

Fork additions live in `x_`-prefixed files and `x/` packages to keep upstream merges cheap.
The only edits to upstream files are these hook points:

| Hook point | Change |
|---|---|
| `backend/app/api/routes.go` | registers `/duplicates/check`, `/import/*`, `/idle/*` and `/build/*` routes |
| `backend/app/api/recurring.go` | hourly `x-idle-digest` task (each collection gets at most one digest per interval) |
| `backend/app/api/main.go` | body-size override for `/api/v1/import/eml` (50 × 10 MB) |
| `backend/internal/data/repo/repos_all.go` | adds `XOrderImport` and `XUseBuild` repositories |
| `backend/internal/sys/config/conf.go` | `OrderImportConf` (`HBOX_ORDER_IMPORT_*`), `AIConf` (`HBOX_AI_*`), `IdleConf` (`HBOX_IDLE_*`) |
| `backend/go.mod` | `golang.org/x/net` promoted to a direct dependency |
| `frontend/lib/api/user.ts` | `xImport` and `xUse` API clients |
| `frontend/layouts/default.vue` | nav entries "Order inbox" (id 90), "Idle items" (91), "Build" (92) |
| `frontend/pages/tag/[id].vue`, `frontend/pages/location/[id]/index/index.vue` | "What can I build?" button (`components/X/BuildButton.vue`) |
| `frontend/pages/item/[id]/index.vue` | "Mark used" button (`components/X/MarkUsedButton.vue`) |
| `frontend/locales/{en,ja-JP}.json` | `menu.order_inbox`, `menu.idle_items`, `menu.build` and the `x_import`, `x_idle`, `x_build` namespaces |
| generated: ent code, swagger docs, `data-contracts.ts` | regenerated via `task --force generate` |

Fork tables (`x_inbound_addresses`, `x_inbound_messages`, `x_import_candidates`, `x_entity_activities`,
`x_idle_digests`, `x_projects`, `x_project_parts`) reference
`groups` / `entities` through FKs declared only in the SQL migrations, so upstream `group.go` and
`entity.go` ent schemas are untouched.

## Order import configuration

- `HBOX_ORDER_IMPORT_INBOUND_DOMAIN`: domain for `orders-<token>@<domain>` forwarding addresses (optional).
- `HBOX_ORDER_IMPORT_LLM_BASE_URL`, `_LLM_API_KEY`, `_LLM_MODEL`, `_LLM_VISION_MODEL`, `_LLM_TIMEOUT_SEC`:
  OpenAI-compatible fallback extractor. Disabled when the base URL or model is unset.
- Webhook: `POST /api/v1/import/inbound/{token}` with raw MIME (≤10 MB), unauthenticated, token-gated.

## Use & build configuration

- `HBOX_AI_BASE_URL`, `HBOX_AI_API_KEY`, `HBOX_AI_MODEL`, `HBOX_AI_TIMEOUT_SEC` (default 90): the shared
  OpenAI-compatible endpoint for "What can I build?" and sale-listing drafts. When unset, the
  `HBOX_ORDER_IMPORT_LLM_*` endpoint is reused. With neither, `/build/suggest` returns 503 and sale
  drafts fall back to a template.
- `HBOX_IDLE_ENABLED` (true), `HBOX_IDLE_DAYS` (180), `HBOX_IDLE_DIGEST_LIMIT` (10),
  `HBOX_IDLE_SNOOZE_DAYS` (90), `HBOX_IDLE_INTERVAL_DAYS` (7). The digest goes through the collection's
  active notifiers; set `HBOX_OPTIONS_HOSTNAME` to include a link to the Idle items page.
- "Last touched" is the latest of created, edited (`entities.updated_at`) and "Mark used". Views are not tracked.

## MVP status

| PR | Branch | Contents | Based on |
|---|---|---|---|
| #1 | `feat/duplicate-check` | `GET/POST /api/v1/duplicates/check` | `main` |
| #2 | `feat/chrome-extension` | MV3 extension in `extension/` (Amazon JP, Rakuten, Yodobashi) | #1 |
| #3 | `feat/order-import` | forwarding webhook, `.eml`/photo upload, store parsers, LLM fallback, review inbox | #1 |
| #4 | `feat/use-and-build` | idle-item digest + Idle items page, "What can I build?", saved projects with reserved parts | #3 |

**Merge order:** #1 → #3 → #4, then #2 (#2 only depends on #1 and can go in any time after it).
Rebase nothing; after each merge, retarget the next PR's base to `main` in the GitHub UI so its diff
shrinks to its own commits.

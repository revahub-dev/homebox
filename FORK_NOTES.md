# Fork notes (revahub-dev/homebox)

Fork additions live in `x_`-prefixed files and `x/` packages to keep upstream merges cheap.
The only edits to upstream files are these hook points:

| Hook point | Change |
|---|---|
| `backend/app/api/routes.go` | registers `/duplicates/check` and `/import/*` routes |
| `backend/app/api/main.go` | body-size override for `/api/v1/import/eml` (50 × 10 MB) |
| `backend/internal/data/repo/repos_all.go` | adds `XOrderImport` repository |
| `backend/internal/sys/config/conf.go` | `OrderImportConf` (`HBOX_ORDER_IMPORT_*`) |
| `backend/go.mod` | `golang.org/x/net` promoted to a direct dependency |
| `frontend/lib/api/user.ts` | `xImport` API client |
| `frontend/layouts/default.vue` | "Order inbox" nav entry (id 90) |
| `frontend/locales/{en,ja-JP}.json` | `menu.order_inbox` and the `x_import` namespace |
| generated: ent code, swagger docs, `data-contracts.ts` | regenerated via `task --force generate` |

Order-import tables (`x_inbound_addresses`, `x_inbound_messages`, `x_import_candidates`) reference
`groups` / `entities` through FKs declared only in the SQL migrations, so upstream `group.go` and
`entity.go` ent schemas are untouched.

## Order import configuration

- `HBOX_ORDER_IMPORT_INBOUND_DOMAIN`: domain for `orders-<token>@<domain>` forwarding addresses (optional).
- `HBOX_ORDER_IMPORT_LLM_BASE_URL`, `_LLM_API_KEY`, `_LLM_MODEL`, `_LLM_VISION_MODEL`, `_LLM_TIMEOUT_SEC`:
  OpenAI-compatible fallback extractor. Disabled when the base URL or model is unset.
- Webhook: `POST /api/v1/import/inbound/{token}` with raw MIME (≤10 MB), unauthenticated, token-gated.

package v1

// Fork addition (order import). See FORK_NOTES.md.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hay-kot/httpkit/errchain"
	"github.com/hay-kot/httpkit/server"
	"github.com/rs/zerolog/log"

	"github.com/sysadminsmedia/homebox/backend/internal/core/services"
	"github.com/sysadminsmedia/homebox/backend/internal/core/services/x/orderimport"
	"github.com/sysadminsmedia/homebox/backend/internal/core/services/x/orderimport/llm"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/config"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/validate"
	"github.com/sysadminsmedia/homebox/backend/internal/web/adapters"
)

const (
	importMaxFiles     = 50
	importInboundPath  = "/api/v1/import/inbound/"
	importAddressLocal = "orders-"
)

var (
	importLLMOnce sync.Once
	importLLM     llm.Extractor
)

func llmFromConfig(c config.OrderImportConf) llm.Extractor {
	return llm.New(llm.Config{
		BaseURL: c.LLMBaseURL, APIKey: c.LLMApiKey, Model: c.LLMModel,
		VisionModel: c.LLMVisionModel, Timeout: time.Duration(c.LLMTimeoutSec) * time.Second,
	})
}

func (ctrl *V1Controller) orderImport() *orderimport.Service {
	svc := &orderimport.Service{Repo: ctrl.repo.XOrderImport, Entities: ctrl.repo.Entities, Attachments: ctrl.repo.Attachments}
	if ctrl.config != nil {
		importLLMOnce.Do(func() { importLLM = llmFromConfig(ctrl.config.OrderImport) })
		svc.LLM = importLLM
	}
	return svc
}

// ImportSettings is the forwarding setup shown on the settings page.
type ImportSettings struct {
	Token string `json:"token"`
	// orders-<token>@<domain>; empty when HBOX_ORDER_IMPORT_INBOUND_DOMAIN is unset.
	Address string `json:"address"`
	// Path of the inbound webhook, relative to the server origin.
	WebhookPath    string     `json:"webhookPath"`
	AllowedSenders []string   `json:"allowedSenders"`
	LastReceivedAt *time.Time `json:"lastReceivedAt,omitempty" extensions:"x-nullable"`
	LLMEnabled     bool       `json:"llmEnabled"`
	MaxBytes       int        `json:"maxBytes"`
	MaxFiles       int        `json:"maxFiles"`
}

// ImportSettingsUpdate replaces the sender allow-list.
type ImportSettingsUpdate struct {
	AllowedSenders []string `json:"allowedSenders" validate:"max=100"`
}

// ImportFileResult is the outcome for one uploaded file.
type ImportFileResult struct {
	FileName string              `json:"fileName"`
	Result   *orderimport.Result `json:"result,omitempty" extensions:"x-nullable"`
	Error    string              `json:"error"`
}

// ImportMergeInput selects the existing entity to merge into.
type ImportMergeInput struct {
	EntityID uuid.UUID `json:"entityId" validate:"required"`
}

// ImportCandidateQuery filters the review inbox.
type ImportCandidateQuery struct {
	Status string `schema:"status" validate:"omitempty,oneof=pending accepted merged ignored"`
}

func (ctrl *V1Controller) importSettings(a repo.XInboundAddress) ImportSettings {
	out := ImportSettings{
		Token: a.Token, WebhookPath: importInboundPath + a.Token, AllowedSenders: a.AllowedSenders,
		LastReceivedAt: a.LastReceivedAt, MaxBytes: orderimport.MaxMessageBytes, MaxFiles: importMaxFiles,
	}
	if ctrl.config != nil {
		if d := strings.TrimSpace(ctrl.config.OrderImport.InboundDomain); d != "" {
			out.Address = importAddressLocal + a.Token + "@" + d
		}
		out.LLMEnabled = llm.Config{BaseURL: ctrl.config.OrderImport.LLMBaseURL, Model: ctrl.config.OrderImport.LLMModel}.Enabled()
	}
	return out
}

// HandleImportSettingsGet godoc
//
//	@Summary	Get order-import forwarding settings
//	@Tags		Order Import
//	@Produce	json
//	@Success	200	{object}	ImportSettings
//	@Router		/v1/import/settings [GET]
//	@Security	Bearer
func (ctrl *V1Controller) HandleImportSettingsGet() errchain.HandlerFunc {
	fn := func(r *http.Request) (ImportSettings, error) {
		auth := services.NewContext(r.Context())
		a, err := ctrl.repo.XOrderImport.GetOrCreateAddress(auth, auth.GID)
		if err != nil {
			return ImportSettings{}, err
		}
		return ctrl.importSettings(a), nil
	}
	return adapters.Command(fn, http.StatusOK)
}

// HandleImportSettingsUpdate godoc
//
//	@Summary	Update the forwarding sender allow-list
//	@Tags		Order Import
//	@Accept		json
//	@Produce	json
//	@Param		payload	body		ImportSettingsUpdate	true	"Allow-list"
//	@Success	200		{object}	ImportSettings
//	@Router		/v1/import/settings [PUT]
//	@Security	Bearer
func (ctrl *V1Controller) HandleImportSettingsUpdate() errchain.HandlerFunc {
	fn := func(r *http.Request, in ImportSettingsUpdate) (ImportSettings, error) {
		auth := services.NewContext(r.Context())
		a, err := ctrl.repo.XOrderImport.SetAllowedSenders(auth, auth.GID, in.AllowedSenders)
		if err != nil {
			return ImportSettings{}, err
		}
		return ctrl.importSettings(a), nil
	}
	return adapters.Action(fn, http.StatusOK)
}

// HandleImportTokenRotate godoc
//
//	@Summary		Rotate the forwarding token
//	@Description	Issues a new token; mail sent to the old address or webhook URL is rejected from now on.
//	@Tags			Order Import
//	@Produce		json
//	@Success		200	{object}	ImportSettings
//	@Router			/v1/import/settings/rotate [POST]
//	@Security		Bearer
func (ctrl *V1Controller) HandleImportTokenRotate() errchain.HandlerFunc {
	fn := func(r *http.Request) (ImportSettings, error) {
		auth := services.NewContext(r.Context())
		a, err := ctrl.repo.XOrderImport.RotateToken(auth, auth.GID)
		if err != nil {
			return ImportSettings{}, err
		}
		return ctrl.importSettings(a), nil
	}
	return adapters.Command(fn, http.StatusOK)
}

// readInboundBody returns raw MIME from a webhook request. Raw bodies are
// accepted as-is; multipart forms (SendGrid "email", Mailgun "body-mime")
// are unwrapped.
func readInboundBody(r *http.Request) ([]byte, error) {
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType != "multipart/form-data" {
		return io.ReadAll(r.Body)
	}
	if err := r.ParseMultipartForm(orderimport.MaxMessageBytes); err != nil {
		return nil, err
	}
	for _, field := range []string{"email", "body-mime", "raw", "message"} {
		if v := r.FormValue(field); v != "" {
			return []byte(v), nil
		}
		if f, _, err := r.FormFile(field); err == nil {
			defer func() { _ = f.Close() }()
			return io.ReadAll(io.LimitReader(f, orderimport.MaxMessageBytes+1))
		}
	}
	return nil, errors.New("no raw MIME field (email, body-mime, raw, message) in form")
}

// HandleImportInbound godoc
//
//	@Summary		Inbound email webhook
//	@Description	Public endpoint for an inbound-email service. The body is the raw MIME message (or a multipart form with an "email"/"body-mime" field). The token in the path authenticates the request. Unknown or rotated tokens get 404, bodies over 10 MB get 413, and senders outside the allow-list get 403. Nothing is imported without review.
//	@Tags			Order Import
//	@Accept			plain
//	@Produce		json
//	@Param			token	path		string	true	"Forwarding token"
//	@Success		200		{object}	orderimport.Result
//	@Router			/v1/import/inbound/{token} [POST]
func (ctrl *V1Controller) HandleImportInbound() errchain.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		token := chi.URLParam(r, "token")
		logger := log.With().Str("component", "order-import-inbound").Str("token_prefix", tokenPrefix(token)).Logger()
		if len(token) < 16 || len(token) > 128 {
			logger.Warn().Msg("rejected: malformed token")
			return validate.NewRequestError(errors.New("not found"), http.StatusNotFound)
		}
		addr, err := ctrl.repo.XOrderImport.AddressByToken(r.Context(), token)
		if err != nil {
			if ent.IsNotFound(err) {
				logger.Warn().Msg("rejected: unknown or rotated token")
				return validate.NewRequestError(errors.New("not found"), http.StatusNotFound)
			}
			return err
		}

		r.Body = http.MaxBytesReader(w, r.Body, orderimport.MaxMessageBytes)
		raw, err := readInboundBody(r)
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) || strings.Contains(err.Error(), "request body too large") {
				logger.Warn().Msg("rejected: message larger than 10 MB")
				return validate.NewRequestError(errors.New("message too large"), http.StatusRequestEntityTooLarge)
			}
			logger.Warn().Err(err).Msg("rejected: unreadable body")
			return validate.NewRequestError(err, http.StatusBadRequest)
		}
		if len(raw) > orderimport.MaxMessageBytes {
			logger.Warn().Msg("rejected: message larger than 10 MB")
			return validate.NewRequestError(errors.New("message too large"), http.StatusRequestEntityTooLarge)
		}

		from, err := orderimport.SenderOf(raw)
		if err != nil {
			logger.Warn().Msg("rejected: not a valid email")
			return validate.NewRequestError(err, http.StatusBadRequest)
		}
		if !orderimport.SenderAllowed(addr.AllowedSenders, from) {
			logger.Warn().Str("sender_domain", domainOf(from)).Msg("rejected: sender not in allow-list")
			return validate.NewRequestError(errors.New("sender not allowed"), http.StatusForbidden)
		}

		res, err := ctrl.orderImport().IngestEmail(r.Context(), addr.GroupID, orderimport.ChannelForward, raw)
		if err != nil {
			if errors.Is(err, orderimport.ErrInvalidEmail) {
				return validate.NewRequestError(err, http.StatusBadRequest)
			}
			return err
		}
		_ = ctrl.repo.XOrderImport.TouchAddress(r.Context(), addr.ID)
		logger.Info().Str("status", res.Status).Int("candidates", res.Candidates).Msg("inbound message processed")
		return server.JSON(w, http.StatusOK, res)
	}
}

func tokenPrefix(t string) string {
	if len(t) > 4 {
		return t[:4] + "…"
	}
	return "…"
}

func domainOf(addr string) string {
	if i := strings.LastIndexByte(addr, '@'); i >= 0 {
		return addr[i+1:]
	}
	return ""
}

// HandleImportEmlUpload godoc
//
//	@Summary		Upload order emails (.eml)
//	@Description	Up to 50 files per request in the "files" field, each at most 10 MB. Every file gets its own result; invalid files report an error without failing the batch.
//	@Tags			Order Import
//	@Accept			multipart/form-data
//	@Produce		json
//	@Param			files	formData	file	true	".eml files"
//	@Success		200		{object}	[]ImportFileResult
//	@Router			/v1/import/eml [POST]
//	@Security		Bearer
func (ctrl *V1Controller) HandleImportEmlUpload() errchain.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		if err := r.ParseMultipartForm(ctrl.maxParseMemory << 20); err != nil {
			return multipartFormError(err)
		}
		files := r.MultipartForm.File["files"]
		if len(files) == 0 {
			return validate.NewRequestError(errors.New("no files uploaded (field \"files\")"), http.StatusUnprocessableEntity)
		}
		if len(files) > importMaxFiles {
			return validate.NewRequestError(fmt.Errorf("at most %d files per upload", importMaxFiles), http.StatusUnprocessableEntity)
		}
		auth := services.NewContext(r.Context())
		svc := ctrl.orderImport()
		out := make([]ImportFileResult, 0, len(files))
		for _, fh := range files {
			res := ImportFileResult{FileName: fh.Filename}
			if fh.Size > orderimport.MaxMessageBytes {
				res.Error = "file larger than 10 MB"
				out = append(out, res)
				continue
			}
			f, err := fh.Open()
			if err != nil {
				res.Error = "could not read file"
				out = append(out, res)
				continue
			}
			raw, err := io.ReadAll(io.LimitReader(f, orderimport.MaxMessageBytes+1))
			_ = f.Close()
			if err != nil {
				res.Error = "could not read file"
				out = append(out, res)
				continue
			}
			r, err := svc.IngestEmail(auth, auth.GID, orderimport.ChannelEML, raw)
			if err != nil {
				if errors.Is(err, orderimport.ErrInvalidEmail) {
					res.Error = err.Error()
				} else {
					log.Err(err).Str("file", fh.Filename).Msg("order import: eml ingest failed")
					res.Error = "import failed"
				}
			} else {
				res.Result = &r
			}
			out = append(out, res)
		}
		return server.JSON(w, http.StatusOK, out)
	}
}

// HandleImportPhotoUpload godoc
//
//	@Summary		Upload a receipt or order screenshot
//	@Description	Reads the image with the configured vision LLM. Results are marked low-confidence. Returns 503 when no LLM is configured.
//	@Tags			Order Import
//	@Accept			multipart/form-data
//	@Produce		json
//	@Param			file	formData	file	true	"JPEG, PNG, WebP or GIF image"
//	@Success		200		{object}	orderimport.Result
//	@Router			/v1/import/photo [POST]
//	@Security		Bearer
func (ctrl *V1Controller) HandleImportPhotoUpload() errchain.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		if err := r.ParseMultipartForm(ctrl.maxParseMemory << 20); err != nil {
			return multipartFormError(err)
		}
		f, fh, err := r.FormFile("file")
		if err != nil {
			return validate.NewRequestError(errors.New("file is required"), http.StatusUnprocessableEntity)
		}
		defer func() { _ = f.Close() }()
		data, err := io.ReadAll(io.LimitReader(f, orderimport.MaxMessageBytes+1))
		if err != nil {
			return err
		}
		mimeType := http.DetectContentType(data)
		if ct := fh.Header.Get("Content-Type"); mimeType == "application/octet-stream" && ct != "" {
			mimeType = ct
		}
		auth := services.NewContext(r.Context())
		res, err := ctrl.orderImport().IngestPhoto(auth, auth.GID, mimeType, data)
		switch {
		case errors.Is(err, orderimport.ErrVisionDisabled):
			return validate.NewRequestError(err, http.StatusServiceUnavailable)
		case errors.Is(err, orderimport.ErrNotAnImage):
			return validate.NewRequestError(err, http.StatusUnsupportedMediaType)
		case err != nil:
			return err
		}
		return server.JSON(w, http.StatusOK, res)
	}
}

// HandleImportCandidates godoc
//
//	@Summary	List the review inbox
//	@Tags		Order Import
//	@Produce	json
//	@Param		status	query		string	false	"pending | accepted | merged | ignored (default: all)"
//	@Success	200		{object}	[]repo.XImportCandidate
//	@Router		/v1/import/candidates [GET]
//	@Security	Bearer
func (ctrl *V1Controller) HandleImportCandidates() errchain.HandlerFunc {
	fn := func(r *http.Request, q ImportCandidateQuery) ([]repo.XImportCandidate, error) {
		auth := services.NewContext(r.Context())
		return ctrl.repo.XOrderImport.ListCandidates(auth, auth.GID, q.Status, 0)
	}
	return adapters.Query(fn, http.StatusOK)
}

func reviewError(err error) error {
	switch {
	case errors.Is(err, repo.ErrCandidateNotPending):
		return validate.NewRequestError(err, http.StatusConflict)
	case errors.Is(err, orderimport.ErrEntityNotFound):
		return validate.NewRequestError(err, http.StatusNotFound)
	}
	return err
}

// HandleImportAccept godoc
//
//	@Summary		Accept a review-inbox item
//	@Description	Creates an entity with purchase date, store and price, and ASIN/JAN custom fields. Optional overrides edit the item before it is created.
//	@Tags			Order Import
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string					true	"Candidate ID"
//	@Param			payload	body		orderimport.AcceptInput	false	"Overrides"
//	@Success		201		{object}	repo.EntityOut
//	@Router			/v1/import/candidates/{id}/accept [POST]
//	@Security		Bearer
func (ctrl *V1Controller) HandleImportAccept() errchain.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := adapters.RouteUUID(r, "id")
		if err != nil {
			return err
		}
		var in orderimport.AcceptInput
		// The body is optional: an empty body accepts the extracted data as-is.
		if body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)); err != nil {
			return err
		} else if len(bytes.TrimSpace(body)) > 0 {
			if err := json.Unmarshal(body, &in); err != nil {
				return validate.NewRequestError(fmt.Errorf("invalid body: %w", err), http.StatusBadRequest)
			}
			if err := validate.Check(in); err != nil {
				return err
			}
		}
		auth := services.NewContext(r.Context())
		out, err := ctrl.orderImport().Accept(auth, auth.GID, id, in)
		if err != nil {
			return reviewError(err)
		}
		return server.JSON(w, http.StatusCreated, out)
	}
}

// HandleImportMerge godoc
//
//	@Summary		Merge a review-inbox item into an existing entity
//	@Description	Adds the quantity, fills in empty purchase fields, and adds ASIN/JAN custom fields.
//	@Tags			Order Import
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string				true	"Candidate ID"
//	@Param			payload	body		ImportMergeInput	true	"Target entity"
//	@Success		200		{object}	repo.EntityOut
//	@Router			/v1/import/candidates/{id}/merge [POST]
//	@Security		Bearer
func (ctrl *V1Controller) HandleImportMerge() errchain.HandlerFunc {
	fn := func(r *http.Request, id uuid.UUID, in ImportMergeInput) (repo.EntityOut, error) {
		auth := services.NewContext(r.Context())
		out, err := ctrl.orderImport().Merge(auth, auth.GID, id, in.EntityID)
		return out, reviewError(err)
	}
	return adapters.ActionID("id", fn, http.StatusOK)
}

// HandleImportIgnore godoc
//
//	@Summary	Ignore a review-inbox entry
//	@Tags		Order Import
//	@Param		id	path	string	true	"Candidate ID"
//	@Success	204
//	@Router		/v1/import/candidates/{id}/ignore [POST]
//	@Security	Bearer
func (ctrl *V1Controller) HandleImportIgnore() errchain.HandlerFunc {
	fn := func(r *http.Request, id uuid.UUID) (any, error) {
		auth := services.NewContext(r.Context())
		return nil, reviewError(ctrl.orderImport().Ignore(auth, auth.GID, id))
	}
	return adapters.CommandID("id", fn, http.StatusNoContent)
}

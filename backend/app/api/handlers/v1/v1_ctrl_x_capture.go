package v1

// Fork addition (photo-first capture). See FORK_NOTES.md.

import (
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/hay-kot/httpkit/errchain"
	"github.com/hay-kot/httpkit/server"

	"github.com/sysadminsmedia/homebox/backend/internal/core/services"
	"github.com/sysadminsmedia/homebox/backend/internal/core/services/x/aichat"
	"github.com/sysadminsmedia/homebox/backend/internal/core/services/x/capture"
	"github.com/sysadminsmedia/homebox/backend/internal/core/services/x/vision"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/config"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/validate"
	"github.com/sysadminsmedia/homebox/backend/internal/web/adapters"
)

// NewCaptureService wires the capture pipeline from HBOX_AI_* and
// HBOX_CAPTURE_*. chat may be nil to build from config. The provider is nil
// (feature disabled) when no endpoint is configured.
func NewCaptureService(repos *repo.AllRepos, cfg *config.Config, chat aichat.Chatter) *capture.Service {
	svc := &capture.Service{
		Repo: repos.XCapture, Entities: repos.Entities, EntityTypes: repos.EntityTypes,
		Tags: repos.Tags, Attachments: repos.Attachments,
	}
	if cfg == nil {
		return svc
	}
	a := cfg.AIResolved()
	if chat == nil {
		chat = AIChatFromConfig(cfg)
	}
	if p := vision.NewOpenAI(chat, "openai-compatible"); p != nil {
		svc.Provider = p
	}
	workers := cfg.Capture.Workers
	if workers < 1 {
		workers = 1
	}
	svc.Cfg = capture.Config{
		Workers: workers, MaxPhotosPerDay: cfg.Capture.MaxPhotosPerDay, MaxPhotoBytes: cfg.Capture.MaxPhotoMB << 20,
		RetentionDays: cfg.Capture.RetentionDays, Language: cfg.Capture.Language, StaleAfter: staleAfter(a.TimeoutSec),
		Model: firstNonEmptyStr(a.VisionModel, a.Model), BaseURL: a.BaseURL,
	}
	return svc
}

func staleAfter(timeoutSec int) time.Duration {
	d := 2 * time.Duration(timeoutSec) * time.Second
	if d < 5*time.Minute {
		d = 5 * time.Minute
	}
	return d
}

func firstNonEmptyStr(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func (ctrl *V1Controller) captureSvc() *capture.Service {
	ctrl.captureOnce.Do(func() {
		if ctrl.capture == nil {
			ctrl.capture = NewCaptureService(ctrl.repo, ctrl.config, nil)
		}
	})
	return ctrl.capture
}

func captureError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, capture.ErrDisabled):
		return validate.NewRequestError(errors.New("photo capture needs an AI endpoint: set HBOX_AI_BASE_URL and HBOX_AI_MODEL"), http.StatusServiceUnavailable)
	case errors.Is(err, capture.ErrTooLarge):
		return validate.NewRequestError(err, http.StatusRequestEntityTooLarge)
	case errors.Is(err, capture.ErrNotImage):
		return validate.NewRequestError(err, http.StatusUnsupportedMediaType)
	case errors.Is(err, capture.ErrDailyCap):
		return validate.NewRequestError(err, http.StatusTooManyRequests)
	case errors.Is(err, capture.ErrBadLocation), errors.Is(err, capture.ErrBadMode), errors.Is(err, capture.ErrBadItemInput):
		return validate.NewRequestError(err, http.StatusUnprocessableEntity)
	case errors.Is(err, capture.ErrBadTarget), ent.IsNotFound(err):
		return validate.NewRequestError(errors.New("not found"), http.StatusNotFound)
	case errors.Is(err, repo.ErrCaptureItemNotPending), errors.Is(err, repo.ErrCaptureSessionClosed), errors.Is(err, capture.ErrNotUndoable):
		return validate.NewRequestError(err, http.StatusConflict)
	}
	return err
}

// CaptureSessionCreate starts a capture session.
type CaptureSessionCreate struct {
	Mode       string     `json:"mode"       validate:"required,oneof=item shelf"`
	LocationID *uuid.UUID `json:"locationId" extensions:"x-nullable"`
}

// CaptureMerge names the item to merge into.
type CaptureMerge struct {
	EntityID uuid.UUID `json:"entityId" validate:"required"`
}

// HandleCaptureStatus godoc
//
//	@Summary		Photo capture status
//	@Description	Whether an AI endpoint is configured, which one (never the key), whether it looks local or cloud, and the queue depth.
//	@Tags			Photo Capture
//	@Produce		json
//	@Success		200	{object}	capture.Status
//	@Router			/v1/capture/status [GET]
//	@Security		Bearer
func (ctrl *V1Controller) HandleCaptureStatus() errchain.HandlerFunc {
	fn := func(r *http.Request) (capture.Status, error) {
		return ctrl.captureSvc().Status(r.Context()), nil
	}
	return adapters.Command(fn, http.StatusOK)
}

// HandleCaptureSessionCreate godoc
//
//	@Summary	Start a capture session
//	@Tags		Photo Capture
//	@Accept		json
//	@Produce	json
//	@Param		payload	body		v1.CaptureSessionCreate	true	"Mode and optional current location"
//	@Success	201		{object}	capture.SessionOut
//	@Router		/v1/capture/sessions [POST]
//	@Security	Bearer
func (ctrl *V1Controller) HandleCaptureSessionCreate() errchain.HandlerFunc {
	fn := func(r *http.Request, in CaptureSessionCreate) (capture.SessionOut, error) {
		auth := services.NewContext(r.Context())
		out, err := ctrl.captureSvc().StartSession(auth, auth.GID, auth.UID, in.Mode, in.LocationID)
		return out, captureError(err)
	}
	return adapters.Action(fn, http.StatusCreated)
}

// HandleCaptureSessionGet godoc
//
//	@Summary		Get a capture session
//	@Description	Photos with their processing state and the detected items. The client polls this while photos are processing.
//	@Tags			Photo Capture
//	@Produce		json
//	@Param			id	path		string	true	"Session ID"
//	@Success		200	{object}	capture.SessionOut
//	@Router			/v1/capture/sessions/{id} [GET]
//	@Security		Bearer
func (ctrl *V1Controller) HandleCaptureSessionGet() errchain.HandlerFunc {
	fn := func(r *http.Request, id uuid.UUID) (capture.SessionOut, error) {
		auth := services.NewContext(r.Context())
		out, err := ctrl.captureSvc().Session(auth, auth.GID, id)
		return out, captureError(err)
	}
	return adapters.CommandID("id", fn, http.StatusOK)
}

// HandleCaptureSessionFinish godoc
//
//	@Summary	Finish a capture session
//	@Tags		Photo Capture
//	@Param		id	path	string	true	"Session ID"
//	@Success	204
//	@Router		/v1/capture/sessions/{id}/finish [POST]
//	@Security	Bearer
func (ctrl *V1Controller) HandleCaptureSessionFinish() errchain.HandlerFunc {
	fn := func(r *http.Request, id uuid.UUID) (any, error) {
		auth := services.NewContext(r.Context())
		return nil, captureError(ctrl.captureSvc().FinishSession(auth, auth.GID, id))
	}
	return adapters.CommandID("id", fn, http.StatusNoContent)
}

// HandleCapturePhotoUpload godoc
//
//	@Summary		Upload a photo to a capture session
//	@Description	Queues the photo for analysis and returns immediately (202). Accepts JPEG, PNG, WebP and HEIC; the photo is re-encoded and EXIF is stripped. 503 when no AI endpoint is configured, 415 for non-images, 413 when too large, 429 at the daily limit.
//	@Tags			Photo Capture
//	@Accept			multipart/form-data
//	@Produce		json
//	@Param			id		path		string	true	"Session ID"
//	@Param			file	formData	file	true	"Photo"
//	@Success		202		{object}	capture.PhotoOut
//	@Router			/v1/capture/sessions/{id}/photos [POST]
//	@Security		Bearer
func (ctrl *V1Controller) HandleCapturePhotoUpload() errchain.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := adapters.RouteUUID(r, "id")
		if err != nil {
			return err
		}
		svc := ctrl.captureSvc()
		if !svc.Enabled() { // fail before reading the body
			return captureError(capture.ErrDisabled)
		}
		if err := r.ParseMultipartForm(ctrl.maxParseMemory << 20); err != nil {
			return multipartFormError(err)
		}
		f, _, err := r.FormFile("file")
		if err != nil {
			return validate.NewRequestError(errors.New("file is required"), http.StatusUnprocessableEntity)
		}
		defer func() { _ = f.Close() }()
		limit := int64(svc.Cfg.MaxPhotoBytes)
		if limit <= 0 {
			limit = 8 << 20
		}
		data, err := io.ReadAll(io.LimitReader(f, limit+1))
		if err != nil {
			return err
		}
		auth := services.NewContext(r.Context())
		out, _, err := svc.AddPhoto(auth, auth.GID, id, data)
		if err != nil {
			return captureError(err)
		}
		return server.JSON(w, http.StatusAccepted, out)
	}
}

// HandleCapturePhotoRetry godoc
//
//	@Summary	Retry a failed photo
//	@Tags		Photo Capture
//	@Param		id	path	string	true	"Photo ID"
//	@Success	204
//	@Router		/v1/capture/photos/{id}/retry [POST]
//	@Security	Bearer
func (ctrl *V1Controller) HandleCapturePhotoRetry() errchain.HandlerFunc {
	fn := func(r *http.Request, id uuid.UUID) (any, error) {
		auth := services.NewContext(r.Context())
		return nil, captureError(ctrl.captureSvc().RetryPhoto(auth, auth.GID, id))
	}
	return adapters.CommandID("id", fn, http.StatusNoContent)
}

// HandleCaptureSessionAccept godoc
//
//	@Summary		Accept all ready items
//	@Description	Accepts every pending item that is high-confidence and has no identifier or identical-name duplicate.
//	@Tags			Photo Capture
//	@Produce		json
//	@Param			id	path		string	true	"Session ID"
//	@Success		200	{object}	capture.BulkResult
//	@Router			/v1/capture/sessions/{id}/accept [POST]
//	@Security		Bearer
func (ctrl *V1Controller) HandleCaptureSessionAccept() errchain.HandlerFunc {
	fn := func(r *http.Request, id uuid.UUID) (capture.BulkResult, error) {
		auth := services.NewContext(r.Context())
		out, err := ctrl.captureSvc().AcceptReady(auth, auth.GID, id)
		return out, captureError(err)
	}
	return adapters.CommandID("id", fn, http.StatusOK)
}

// HandleCaptureItemCrop godoc
//
//	@Summary	Crop image of a detected item
//	@Tags		Photo Capture
//	@Produce	image/jpeg
//	@Param		id	path	string	true	"Item ID"
//	@Success	200	{file}	binary
//	@Router		/v1/capture/items/{id}/crop [GET]
//	@Security	Bearer
func (ctrl *V1Controller) HandleCaptureItemCrop() errchain.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := adapters.RouteUUID(r, "id")
		if err != nil {
			return err
		}
		auth := services.NewContext(r.Context())
		b, err := ctrl.captureSvc().ItemCrop(auth, auth.GID, id)
		if err != nil {
			return captureError(err)
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "private, max-age=3600")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, err = w.Write(b)
		return err
	}
}

// HandleCaptureItemEdit godoc
//
//	@Summary	Edit a pending item
//	@Tags		Photo Capture
//	@Accept		json
//	@Produce	json
//	@Param		id		path		string				true	"Item ID"
//	@Param		payload	body		capture.EditInput	true	"Fields to change"
//	@Success	200		{object}	capture.ItemOut
//	@Router		/v1/capture/items/{id} [PATCH]
//	@Security	Bearer
func (ctrl *V1Controller) HandleCaptureItemEdit() errchain.HandlerFunc {
	fn := func(r *http.Request, id uuid.UUID, in capture.EditInput) (capture.ItemOut, error) {
		auth := services.NewContext(r.Context())
		out, err := ctrl.captureSvc().Edit(auth, auth.GID, id, in)
		return out, captureError(err)
	}
	return adapters.ActionID("id", fn, http.StatusOK)
}

// HandleCaptureItemAccept godoc
//
//	@Summary		Accept a detected item
//	@Description	Creates the inventory item in the chosen location (or "Unsorted") with the crop as its photo.
//	@Tags			Photo Capture
//	@Produce		json
//	@Param			id	path		string	true	"Item ID"
//	@Success		200	{object}	capture.AcceptResult
//	@Router			/v1/capture/items/{id}/accept [POST]
//	@Security		Bearer
func (ctrl *V1Controller) HandleCaptureItemAccept() errchain.HandlerFunc {
	fn := func(r *http.Request, id uuid.UUID) (capture.AcceptResult, error) {
		auth := services.NewContext(r.Context())
		out, err := ctrl.captureSvc().Accept(auth, auth.GID, id)
		return out, captureError(err)
	}
	return adapters.CommandID("id", fn, http.StatusOK)
}

// HandleCaptureItemMerge godoc
//
//	@Summary		Merge a detected item into an existing one
//	@Description	Adds the detected quantity to an existing item instead of creating a duplicate.
//	@Tags			Photo Capture
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string			true	"Item ID"
//	@Param			payload	body		v1.CaptureMerge	true	"Existing item"
//	@Success		200		{object}	capture.AcceptResult
//	@Router			/v1/capture/items/{id}/merge [POST]
//	@Security		Bearer
func (ctrl *V1Controller) HandleCaptureItemMerge() errchain.HandlerFunc {
	fn := func(r *http.Request, id uuid.UUID, in CaptureMerge) (capture.AcceptResult, error) {
		auth := services.NewContext(r.Context())
		out, err := ctrl.captureSvc().Merge(auth, auth.GID, id, in.EntityID)
		return out, captureError(err)
	}
	return adapters.ActionID("id", fn, http.StatusOK)
}

// HandleCaptureItemIgnore godoc
//
//	@Summary	Ignore a detected item
//	@Tags		Photo Capture
//	@Produce	json
//	@Param		id	path		string	true	"Item ID"
//	@Success	200	{object}	capture.ItemOut
//	@Router		/v1/capture/items/{id}/ignore [POST]
//	@Security	Bearer
func (ctrl *V1Controller) HandleCaptureItemIgnore() errchain.HandlerFunc {
	fn := func(r *http.Request, id uuid.UUID) (capture.ItemOut, error) {
		auth := services.NewContext(r.Context())
		out, err := ctrl.captureSvc().Ignore(auth, auth.GID, id)
		return out, captureError(err)
	}
	return adapters.CommandID("id", fn, http.StatusOK)
}

// HandleCaptureItemUndo godoc
//
//	@Summary		Undo accept, merge or ignore
//	@Description	Deletes the created item, restores the merged quantity, or un-ignores. Works for 30 minutes after the decision.
//	@Tags			Photo Capture
//	@Produce		json
//	@Param			id	path		string	true	"Item ID"
//	@Success		200	{object}	capture.ItemOut
//	@Router			/v1/capture/items/{id}/undo [POST]
//	@Security		Bearer
func (ctrl *V1Controller) HandleCaptureItemUndo() errchain.HandlerFunc {
	fn := func(r *http.Request, id uuid.UUID) (capture.ItemOut, error) {
		auth := services.NewContext(r.Context())
		out, err := ctrl.captureSvc().Undo(auth, auth.GID, id)
		return out, captureError(err)
	}
	return adapters.CommandID("id", fn, http.StatusOK)
}

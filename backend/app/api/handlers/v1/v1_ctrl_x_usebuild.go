package v1

// Fork addition (use & build: idle-item nudges, "What can I build?").
// See FORK_NOTES.md.

import (
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/hay-kot/httpkit/errchain"

	"github.com/sysadminsmedia/homebox/backend/internal/core/services"
	"github.com/sysadminsmedia/homebox/backend/internal/core/services/x/aichat"
	"github.com/sysadminsmedia/homebox/backend/internal/core/services/x/usebuild"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/entity"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/entitytype"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/group"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/tag"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/config"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/validate"
	"github.com/sysadminsmedia/homebox/backend/internal/web/adapters"
)

var (
	aiChatOnce sync.Once
	aiChat     aichat.Chatter
	// aiChatOverride lets tests inject a fake model.
	aiChatOverride aichat.Chatter
)

// AIChatFromConfig builds the shared chat client (HBOX_AI_*, falling back to
// HBOX_ORDER_IMPORT_LLM_*). Returns nil when nothing is configured.
func AIChatFromConfig(c *config.Config) aichat.Chatter {
	if c == nil {
		return nil
	}
	a := c.AIResolved()
	return aichat.New(aichat.Config{BaseURL: a.BaseURL, APIKey: a.APIKey, Model: a.Model, VisionModel: a.VisionModel, ResponseFormat: a.ResponseFormat, Timeout: time.Duration(a.TimeoutSec) * time.Second})
}

func (ctrl *V1Controller) aiChat() aichat.Chatter {
	if aiChatOverride != nil {
		return aiChatOverride
	}
	aiChatOnce.Do(func() { aiChat = AIChatFromConfig(ctrl.config) })
	return aiChat
}

func (ctrl *V1Controller) idleConf() config.IdleConf {
	if ctrl.config == nil {
		return usebuild.IdleSettings(config.IdleConf{Enabled: true})
	}
	return usebuild.IdleSettings(ctrl.config.Idle)
}

func useBuildError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, repo.ErrNotAnItem), ent.IsNotFound(err):
		return validate.NewRequestError(errors.New("not found"), http.StatusNotFound)
	case errors.Is(err, aichat.ErrDisabled):
		return validate.NewRequestError(err, http.StatusServiceUnavailable)
	case errors.Is(err, usebuild.ErrEmptyInventory):
		return validate.NewRequestError(err, http.StatusUnprocessableEntity)
	case errors.Is(err, usebuild.ErrBadModelReply):
		return validate.NewRequestError(err, http.StatusBadGateway)
	}
	return err
}

// ---------------------------------------------------------------------------
// Idle items

// IdleQuery filters the idle list.
type IdleQuery struct {
	Limit int `schema:"limit" validate:"omitempty,min=1,max=500"`
}

// IdleList is the Idle items page payload.
type IdleList struct {
	Days       int             `json:"days"`
	SnoozeDays int             `json:"snoozeDays"`
	AIEnabled  bool            `json:"aiEnabled"`
	Items      []repo.XInvItem `json:"items"`
	ForSale    []repo.XInvItem `json:"forSale"`
}

// SaleDraftOut is the listing draft for an item marked for sale.
type SaleDraftOut struct {
	Draft          string `json:"draft"`
	SuggestedPrice int    `json:"suggestedPrice"`
	Generated      bool   `json:"generated"`
}

// HandleIdleList godoc
//
//	@Summary	List idle items (untouched for HBOX_IDLE_DAYS) and items marked for sale
//	@Tags		Use & Build
//	@Produce	json
//	@Param		limit	query		int	false	"max idle items (default 100)"
//	@Success	200		{object}	IdleList
//	@Router		/v1/idle [GET]
//	@Security	Bearer
func (ctrl *V1Controller) HandleIdleList() errchain.HandlerFunc {
	fn := func(r *http.Request, q IdleQuery) (IdleList, error) {
		auth := services.NewContext(r.Context())
		conf := ctrl.idleConf()
		if q.Limit == 0 {
			q.Limit = 100
		}
		now := time.Now()
		items, err := ctrl.repo.XUseBuild.IdleItems(auth, auth.GID, now.Add(-time.Duration(conf.Days)*24*time.Hour), now, q.Limit)
		if err != nil {
			return IdleList{}, err
		}
		sale, err := ctrl.repo.XUseBuild.ForSaleItems(auth, auth.GID)
		if err != nil {
			return IdleList{}, err
		}
		return IdleList{Days: conf.Days, SnoozeDays: conf.SnoozeDays, AIEnabled: ctrl.aiChat() != nil, Items: items, ForSale: sale}, nil
	}
	return adapters.Query(fn, http.StatusOK)
}

// HandleItemUse godoc
//
//	@Summary	Mark an item as used (resets its idle timer)
//	@Tags		Use & Build
//	@Param		id	path	string	true	"Entity ID"
//	@Success	204
//	@Router		/v1/idle/{id}/use [POST]
//	@Security	Bearer
func (ctrl *V1Controller) HandleItemUse() errchain.HandlerFunc {
	fn := func(r *http.Request, id uuid.UUID) (any, error) {
		auth := services.NewContext(r.Context())
		return nil, useBuildError(ctrl.repo.XUseBuild.MarkUsed(auth, auth.GID, id, time.Now()))
	}
	return adapters.CommandID("id", fn, http.StatusNoContent)
}

// HandleItemKeep godoc
//
//	@Summary	Keep an idle item (snooze nudges for HBOX_IDLE_SNOOZE_DAYS, default 90)
//	@Tags		Use & Build
//	@Param		id	path	string	true	"Entity ID"
//	@Success	204
//	@Router		/v1/idle/{id}/keep [POST]
//	@Security	Bearer
func (ctrl *V1Controller) HandleItemKeep() errchain.HandlerFunc {
	fn := func(r *http.Request, id uuid.UUID) (any, error) {
		auth := services.NewContext(r.Context())
		until := time.Now().Add(time.Duration(ctrl.idleConf().SnoozeDays) * 24 * time.Hour)
		return nil, useBuildError(ctrl.repo.XUseBuild.Snooze(auth, auth.GID, id, until))
	}
	return adapters.CommandID("id", fn, http.StatusNoContent)
}

// HandleItemArchive godoc
//
//	@Summary	Archive an idle item
//	@Tags		Use & Build
//	@Param		id	path	string	true	"Entity ID"
//	@Success	204
//	@Router		/v1/idle/{id}/archive [POST]
//	@Security	Bearer
func (ctrl *V1Controller) HandleItemArchive() errchain.HandlerFunc {
	fn := func(r *http.Request, id uuid.UUID) (any, error) {
		auth := services.NewContext(r.Context())
		return nil, useBuildError(ctrl.repo.XUseBuild.Archive(auth, auth.GID, id))
	}
	return adapters.CommandID("id", fn, http.StatusNoContent)
}

// HandleItemSell godoc
//
//	@Summary		Mark an item for sale and draft a Mercari-style listing
//	@Description	Uses the LLM when configured, otherwise a template. Nothing is posted anywhere.
//	@Tags			Use & Build
//	@Produce		json
//	@Param			id	path		string	true	"Entity ID"
//	@Success		200	{object}	SaleDraftOut
//	@Router			/v1/idle/{id}/sell [POST]
//	@Security		Bearer
func (ctrl *V1Controller) HandleItemSell() errchain.HandlerFunc {
	fn := func(r *http.Request, id uuid.UUID) (SaleDraftOut, error) {
		auth := services.NewContext(r.Context())
		inv, err := ctrl.repo.XUseBuild.Inventory(auth, auth.GID, repo.XInventoryScope{})
		if err != nil {
			return SaleDraftOut{}, err
		}
		var it *repo.XInvItem
		for i := range inv {
			if inv[i].ID == id {
				it = &inv[i]
				break
			}
		}
		if it == nil {
			return SaleDraftOut{}, useBuildError(repo.ErrNotAnItem)
		}
		chat := ctrl.aiChat()
		draft := usebuild.SaleDraft(auth, chat, *it)
		if err := ctrl.repo.XUseBuild.SetForSale(auth, auth.GID, id, true, draft); err != nil {
			return SaleDraftOut{}, useBuildError(err)
		}
		return SaleDraftOut{Draft: draft, SuggestedPrice: usebuild.SuggestedPrice(it.PurchasePrice), Generated: chat != nil}, nil
	}
	return adapters.CommandID("id", fn, http.StatusOK)
}

// HandleItemUnsell godoc
//
//	@Summary	Remove the for-sale mark from an item
//	@Tags		Use & Build
//	@Param		id	path	string	true	"Entity ID"
//	@Success	204
//	@Router		/v1/idle/{id}/unsell [POST]
//	@Security	Bearer
func (ctrl *V1Controller) HandleItemUnsell() errchain.HandlerFunc {
	fn := func(r *http.Request, id uuid.UUID) (any, error) {
		auth := services.NewContext(r.Context())
		return nil, useBuildError(ctrl.repo.XUseBuild.SetForSale(auth, auth.GID, id, false, ""))
	}
	return adapters.CommandID("id", fn, http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// What can I build?

// BuildSuggestInput selects the items sent to the model. Leave both empty
// for the whole inventory.
type BuildSuggestInput struct {
	TagID      *uuid.UUID `json:"tagId,omitempty"      extensions:"x-nullable"`
	LocationID *uuid.UUID `json:"locationId,omitempty" extensions:"x-nullable"`
}

// BuildStatus reports whether the AI endpoint is configured.
type BuildStatus struct {
	AIEnabled bool `json:"aiEnabled"`
}

// HandleBuildStatus godoc
//
//	@Summary	Whether "What can I build?" is available (an LLM is configured)
//	@Tags		Use & Build
//	@Produce	json
//	@Success	200	{object}	BuildStatus
//	@Router		/v1/build/status [GET]
//	@Security	Bearer
func (ctrl *V1Controller) HandleBuildStatus() errchain.HandlerFunc {
	fn := func(r *http.Request) (BuildStatus, error) {
		return BuildStatus{AIEnabled: ctrl.aiChat() != nil}, nil
	}
	return adapters.Command(fn, http.StatusOK)
}

// HandleBuildSuggest godoc
//
//	@Summary		What can I build?
//	@Description	Sends the owned items in scope (names, quantities, descriptions) to the configured OpenAI-compatible LLM and returns 3-5 validated project ideas. Owned parts always reference real item IDs; invented references are dropped. 503 when no LLM is configured.
//	@Tags			Use & Build
//	@Accept			json
//	@Produce		json
//	@Param			payload	body		BuildSuggestInput	true	"Scope"
//	@Success		200		{object}	usebuild.BuildResult
//	@Failure		503		{object}	validate.ErrorResponse
//	@Router			/v1/build/suggest [POST]
//	@Security		Bearer
func (ctrl *V1Controller) HandleBuildSuggest() errchain.HandlerFunc {
	fn := func(r *http.Request, in BuildSuggestInput) (usebuild.BuildResult, error) {
		auth := services.NewContext(r.Context())
		chat := ctrl.aiChat()
		if chat == nil {
			return usebuild.BuildResult{}, useBuildError(aichat.ErrDisabled)
		}
		scope := repo.XInventoryScope{}
		label := "All inventory"
		db := ctrl.repo.XUseBuild.Client()
		if in.TagID != nil && *in.TagID != uuid.Nil {
			t, err := db.Tag.Query().Where(tag.ID(*in.TagID), tag.HasGroupWith(group.ID(auth.GID))).Only(auth)
			if err != nil {
				return usebuild.BuildResult{}, useBuildError(err)
			}
			scope.TagID, label = t.ID, "Tag: "+t.Name
		}
		if in.LocationID != nil && *in.LocationID != uuid.Nil {
			l, err := db.Entity.Query().Where(entity.ID(*in.LocationID), entity.HasGroupWith(group.ID(auth.GID)),
				entity.HasEntityTypeWith(entitytype.IsLocation(true))).Only(auth)
			if err != nil {
				return usebuild.BuildResult{}, useBuildError(err)
			}
			scope.LocationID, label = l.ID, "Location: "+l.Name
		}
		items, err := ctrl.repo.XUseBuild.Inventory(auth, auth.GID, scope)
		if err != nil {
			return usebuild.BuildResult{}, err
		}
		res, err := usebuild.Suggest(auth, chat, items, label)
		return res, useBuildError(err)
	}
	return adapters.Action(fn, http.StatusOK)
}

// HandleProjectsList godoc
//
//	@Summary	List saved projects (with reserved parts)
//	@Tags		Use & Build
//	@Produce	json
//	@Success	200	{object}	[]repo.XProjectOut
//	@Router		/v1/build/projects [GET]
//	@Security	Bearer
func (ctrl *V1Controller) HandleProjectsList() errchain.HandlerFunc {
	fn := func(r *http.Request) ([]repo.XProjectOut, error) {
		auth := services.NewContext(r.Context())
		return ctrl.repo.XUseBuild.ListProjects(auth, auth.GID)
	}
	return adapters.Command(fn, http.StatusOK)
}

// HandleProjectCreate godoc
//
//	@Summary	Save an idea as a project and reserve its owned parts
//	@Tags		Use & Build
//	@Accept		json
//	@Produce	json
//	@Param		payload	body		repo.XProjectIn	true	"Project"
//	@Success	201		{object}	repo.XProjectOut
//	@Router		/v1/build/projects [POST]
//	@Security	Bearer
func (ctrl *V1Controller) HandleProjectCreate() errchain.HandlerFunc {
	fn := func(r *http.Request, in repo.XProjectIn) (repo.XProjectOut, error) {
		auth := services.NewContext(r.Context())
		out, err := ctrl.repo.XUseBuild.CreateProject(auth, auth.GID, in)
		if errors.Is(err, repo.ErrNotAnItem) {
			return out, validate.NewRequestError(err, http.StatusUnprocessableEntity)
		}
		return out, err
	}
	return adapters.Action(fn, http.StatusCreated)
}

// HandleProjectDelete godoc
//
//	@Summary	Delete a project and release its reserved parts
//	@Tags		Use & Build
//	@Param		id	path	string	true	"Project ID"
//	@Success	204
//	@Router		/v1/build/projects/{id} [DELETE]
//	@Security	Bearer
func (ctrl *V1Controller) HandleProjectDelete() errchain.HandlerFunc {
	fn := func(r *http.Request, id uuid.UUID) (any, error) {
		auth := services.NewContext(r.Context())
		return nil, useBuildError(ctrl.repo.XUseBuild.DeleteProject(auth, auth.GID, id))
	}
	return adapters.CommandID("id", fn, http.StatusNoContent)
}

package v1

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/hay-kot/httpkit/errchain"
	"github.com/sysadminsmedia/homebox/backend/internal/core/dupcheck"
	"github.com/sysadminsmedia/homebox/backend/internal/core/services"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/validate"
	"github.com/sysadminsmedia/homebox/backend/internal/web/adapters"
)

const dupCheckMaxLimit = 50

// DuplicateCheckQuery is a prospective purchase to check against the group's inventory.
type DuplicateCheckQuery struct {
	Name        string `json:"name"        schema:"name"        validate:"max=500"`
	ASIN        string `json:"asin"        schema:"asin"        validate:"max=64"`
	JAN         string `json:"jan"         schema:"jan"         validate:"max=64"`
	ModelNumber string `json:"modelNumber" schema:"modelNumber" validate:"max=255"`
	// Maximum number of matches (default 10, max 50).
	Limit int `json:"limit" schema:"limit" validate:"min=0,max=50"`
}

// DuplicateMatch is one ranked existing entity.
type DuplicateMatch struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
	// exact | likely | similar
	Confidence dupcheck.Tier `json:"confidence" enums:"exact,likely,similar"`
	// 0..1; comparable across tiers.
	Score float64 `json:"score"`
	// asin | jan | model_number | normalized_name | token_overlap | semantic
	MatchedOn dupcheck.Reason `json:"matchedOn" enums:"asin,jan,model_number,normalized_name,token_overlap,semantic"`
	// Ancestors from root to direct parent.
	Location []repo.EntityPath `json:"location"`
	// Location rendered as "Garage > Drawer 3".
	LocationPath string `json:"locationPath"`
}

// DuplicateCheckResult is the response of the duplicate check endpoint.
type DuplicateCheckResult struct {
	Matches []DuplicateMatch `json:"matches"`
}

// HandleDuplicateCheckGet godoc
//
//	@Summary		Check for duplicates (query string)
//	@Description	Ranks the group's existing items against a prospective purchase. Order: exact ASIN, JAN or model number; normalized-name match; token-overlap similarity. At least one of name, asin, jan or modelNumber is required.
//	@Tags			Duplicates
//	@Produce		json
//	@Param			name		query		string	false	"Product name / title"
//	@Param			asin		query		string	false	"Amazon ASIN"
//	@Param			jan			query		string	false	"JAN / EAN barcode"
//	@Param			modelNumber	query		string	false	"Model number"
//	@Param			limit		query		int		false	"Max matches (default 10, max 50)"
//	@Success		200			{object}	DuplicateCheckResult
//	@Router			/v1/duplicates/check [GET]
//	@Security		Bearer
func (ctrl *V1Controller) HandleDuplicateCheckGet() errchain.HandlerFunc {
	return adapters.Query(ctrl.duplicateCheck, http.StatusOK)
}

// HandleDuplicateCheckPost godoc
//
//	@Summary		Check for duplicates
//	@Description	Same as the GET variant, with the query in a JSON body.
//	@Tags			Duplicates
//	@Accept			json
//	@Produce		json
//	@Param			payload	body		DuplicateCheckQuery	true	"Prospective purchase"
//	@Success		200		{object}	DuplicateCheckResult
//	@Router			/v1/duplicates/check [POST]
//	@Security		Bearer
func (ctrl *V1Controller) HandleDuplicateCheckPost() errchain.HandlerFunc {
	return adapters.Action(ctrl.duplicateCheck, http.StatusOK)
}

func (ctrl *V1Controller) duplicateCheck(r *http.Request, q DuplicateCheckQuery) (DuplicateCheckResult, error) {
	if strings.TrimSpace(q.Name+q.ASIN+q.JAN+q.ModelNumber) == "" {
		return DuplicateCheckResult{}, validate.NewFieldErrors(
			validate.NewFieldError("name", "one of name, asin, jan or modelNumber is required"),
		)
	}
	auth := services.NewContext(r.Context())
	return runDuplicateCheck(auth, ctrl.repo.Entities, auth.GID, q)
}

func runDuplicateCheck(ctx context.Context, entities *repo.EntityRepository, gid uuid.UUID, q DuplicateCheckQuery) (DuplicateCheckResult, error) {
	cands, err := entities.DupCheckCandidates(ctx, gid)
	if err != nil {
		return DuplicateCheckResult{}, err
	}

	limit := q.Limit
	if limit <= 0 {
		limit = dupcheck.DefaultLimit
	}
	limit = min(limit, dupCheckMaxLimit)

	ranked, err := dupcheck.Rank(ctx, dupcheck.Query{
		Name:        q.Name,
		ASIN:        q.ASIN,
		JAN:         q.JAN,
		ModelNumber: q.ModelNumber,
	}, cands, dupcheck.Options{Limit: limit})
	if err != nil {
		return DuplicateCheckResult{}, err
	}

	out := DuplicateCheckResult{Matches: make([]DuplicateMatch, 0, len(ranked))}
	for _, m := range ranked {
		path, err := entities.PathForEntity(ctx, gid, m.Candidate.ID)
		if err != nil {
			return DuplicateCheckResult{}, err
		}
		// PathForEntity ends with the entity itself; keep only its ancestors.
		if n := len(path); n > 0 && path[n-1].ID == m.Candidate.ID {
			path = path[:n-1]
		}
		if path == nil {
			path = []repo.EntityPath{}
		}
		names := make([]string, len(path))
		for i, p := range path {
			names[i] = p.Name
		}
		out.Matches = append(out.Matches, DuplicateMatch{
			ID:           m.Candidate.ID,
			Name:         m.Candidate.Name,
			Confidence:   m.Tier,
			Score:        m.Score,
			MatchedOn:    m.Reason,
			Location:     path,
			LocationPath: strings.Join(names, " > "),
		})
	}
	return out, nil
}

package repo

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/sysadminsmedia/homebox/backend/internal/core/dupcheck"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/entity"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/entitytype"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/group"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Custom field names (case-insensitive, after trimming) that are read as
// product identifiers for duplicate checking. Homebox has no built-in ASIN or
// JAN column, so these live in custom fields.
var (
	dupASINFieldNames  = map[string]struct{}{"asin": {}}
	dupJANFieldNames   = map[string]struct{}{"jan": {}, "ean": {}, "jan code": {}, "jan/ean": {}, "upc": {}, "barcode": {}, "gtin": {}}
	dupModelFieldNames = map[string]struct{}{"model": {}, "model number": {}, "model no": {}, "型番": {}}
)

// DupCheckCandidates returns every non-location, non-archived entity in the
// group as a duplicate-check candidate, with identifiers gathered from the
// model_number column and from recognised custom fields.
func (r *EntityRepository) DupCheckCandidates(ctx context.Context, gid uuid.UUID) ([]dupcheck.Candidate, error) {
	ctx, span := entityTracer().Start(ctx, "repo.EntityRepository.DupCheckCandidates",
		trace.WithAttributes(attribute.String("group.id", gid.String())))
	defer span.End()

	rows, err := r.db.Entity.Query().
		Where(
			entity.HasGroupWith(group.ID(gid)),
			entity.Archived(false),
			entity.Not(entity.HasEntityTypeWith(entitytype.IsLocation(true))),
		).
		WithFields().
		All(ctx)
	if err != nil {
		recordSpanError(span, err)
		return nil, err
	}

	out := make([]dupcheck.Candidate, 0, len(rows))
	for _, e := range rows {
		c := dupcheck.Candidate{ID: e.ID, Name: e.Name}
		if e.ModelNumber != "" {
			c.ModelNumbers = append(c.ModelNumbers, e.ModelNumber)
		}
		for _, f := range e.Edges.Fields {
			v := strings.TrimSpace(f.TextValue)
			if v == "" {
				continue
			}
			name := strings.ToLower(strings.TrimSpace(f.Name))
			if _, ok := dupASINFieldNames[name]; ok {
				c.ASINs = append(c.ASINs, v)
			}
			if _, ok := dupJANFieldNames[name]; ok {
				c.JANs = append(c.JANs, v)
			}
			if _, ok := dupModelFieldNames[name]; ok {
				c.ModelNumbers = append(c.ModelNumbers, v)
			}
		}
		out = append(out, c)
	}
	span.SetAttributes(attribute.Int("candidates.count", len(out)))
	return out, nil
}

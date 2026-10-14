package repo

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/sysadminsmedia/homebox/backend/internal/core/dupcheck"
)

func TestEntityRepository_DupCheckCandidates(t *testing.T) {
	ctx := context.Background()
	e := useEntities(t, 1)[0]

	upd := EntityUpdate{
		ID:          e.ID,
		Name:        e.Name,
		ModelNumber: "WH-1000XM5",
		Fields: []EntityFieldData{
			{Name: "ASIN", Type: "text", TextValue: "B0DUPTEST1"},
			{Name: "JAN", Type: "text", TextValue: "4901234567894"},
			{Name: "Color", Type: "text", TextValue: "black"},
		},
	}
	if e.EntityType != nil {
		upd.EntityTypeID = e.EntityType.ID
	}
	_, err := tRepos.Entities.UpdateByGroup(ctx, tGroup.ID, upd)
	require.NoError(t, err)

	cands, err := tRepos.Entities.DupCheckCandidates(ctx, tGroup.ID)
	require.NoError(t, err)

	var found *dupcheck.Candidate
	for i := range cands {
		if cands[i].ID == e.ID {
			found = &cands[i]
		}
		// The location container created by useEntities must not be a candidate.
		if e.Parent != nil {
			assert.NotEqual(t, e.Parent.ID, cands[i].ID)
		}
	}
	require.NotNil(t, found)
	assert.Equal(t, []string{"B0DUPTEST1"}, found.ASINs)
	assert.Equal(t, []string{"4901234567894"}, found.JANs)
	assert.Equal(t, []string{"WH-1000XM5"}, found.ModelNumbers)

	ms, err := dupcheck.Rank(ctx, dupcheck.Query{Name: "anything", ASIN: "b0duptest1"}, cands, dupcheck.Options{})
	require.NoError(t, err)
	require.NotEmpty(t, ms)
	assert.Equal(t, e.ID, ms[0].Candidate.ID)
	assert.Equal(t, dupcheck.TierExact, ms[0].Tier)
}

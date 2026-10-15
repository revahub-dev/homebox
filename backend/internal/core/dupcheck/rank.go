package dupcheck

import (
	"context"
	"sort"

	"github.com/google/uuid"
)

// Tier is the coarse confidence bucket for a match.
type Tier string

const (
	TierExact   Tier = "exact"   // an identifier (ASIN, JAN, model number) matched
	TierLikely  Tier = "likely"  // normalized names are identical
	TierSimilar Tier = "similar" // token-overlap (or, later, embedding) similarity
)

// Reason describes which signal produced a match.
type Reason string

const (
	ReasonASIN        Reason = "asin"
	ReasonJAN         Reason = "jan"
	ReasonModelNumber Reason = "model_number"
	ReasonName        Reason = "normalized_name"
	ReasonTokens      Reason = "token_overlap"
	ReasonSemantic    Reason = "semantic"
)

// DefaultMinSimilarity is the lowest token similarity reported as "similar".
const DefaultMinSimilarity = 0.35

// DefaultLimit caps the number of matches returned.
const DefaultLimit = 10

// Query is a prospective purchase.
type Query struct {
	Name        string
	ASIN        string
	JAN         string
	ModelNumber string
}

// Candidate is an existing entity considered for matching. Identifier slices
// hold every value found on the entity (built-in fields and custom fields).
type Candidate struct {
	ID           uuid.UUID
	Name         string
	ASINs        []string
	JANs         []string
	ModelNumbers []string
}

// Match is a ranked candidate.
type Match struct {
	Candidate Candidate
	Tier      Tier
	Score     float64 // 0..1, comparable across tiers
	Reason    Reason
}

// SimilarityScorer is the extension point for semantic (embedding) matching.
// Implementations return a 0..1 similarity per candidate ID; candidates not
// in the map are treated as 0. The default ranker uses token overlap only.
type SimilarityScorer interface {
	Score(ctx context.Context, q Query, cands []Candidate) (map[uuid.UUID]float64, error)
}

// Options tune Rank.
type Options struct {
	Limit         int
	MinSimilarity float64
	// Semantic, when non-nil, contributes an additional similarity signal for
	// candidates without an exact or name match. Nil in this release.
	Semantic SimilarityScorer
}

// Rank orders candidates: exact identifier hits first, then normalized-name
// equality, then token-overlap similarity above opts.MinSimilarity.
func Rank(ctx context.Context, q Query, cands []Candidate, opts Options) ([]Match, error) {
	if opts.Limit <= 0 {
		opts.Limit = DefaultLimit
	}
	if opts.MinSimilarity <= 0 {
		opts.MinSimilarity = DefaultMinSimilarity
	}

	qASIN, qJAN, qModel := NormalizeID(q.ASIN), NormalizeID(q.JAN), NormalizeID(q.ModelNumber)
	qName := NormalizeName(q.Name)
	qTokens := Tokens(q.Name)

	var semantic map[uuid.UUID]float64
	if opts.Semantic != nil {
		var err error
		semantic, err = opts.Semantic.Score(ctx, q, cands)
		if err != nil {
			return nil, err
		}
	}

	matches := make([]Match, 0)
	for _, c := range cands {
		m, ok := scoreOne(c, qASIN, qJAN, qModel, qName, qTokens, opts.MinSimilarity, semantic)
		if ok {
			matches = append(matches, m)
		}
	}

	sort.SliceStable(matches, func(i, j int) bool {
		ti, tj := tierRank(matches[i].Tier), tierRank(matches[j].Tier)
		if ti != tj {
			return ti < tj
		}
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		return matches[i].Candidate.Name < matches[j].Candidate.Name
	})
	if len(matches) > opts.Limit {
		matches = matches[:opts.Limit]
	}
	return matches, nil
}

func scoreOne(c Candidate, qASIN, qJAN, qModel, qName string, qTokens []string, minSim float64, semantic map[uuid.UUID]float64) (Match, bool) {
	switch {
	case qASIN != "" && containsID(c.ASINs, qASIN):
		return Match{Candidate: c, Tier: TierExact, Score: 1, Reason: ReasonASIN}, true
	case qJAN != "" && containsID(c.JANs, qJAN):
		return Match{Candidate: c, Tier: TierExact, Score: 0.995, Reason: ReasonJAN}, true
	case qModel != "" && containsID(c.ModelNumbers, qModel):
		return Match{Candidate: c, Tier: TierExact, Score: 0.99, Reason: ReasonModelNumber}, true
	}

	if qName != "" && NormalizeName(c.Name) == qName {
		return Match{Candidate: c, Tier: TierLikely, Score: 0.9, Reason: ReasonName}, true
	}

	sim := TokenSimilarity(qTokens, Tokens(c.Name))
	reason := ReasonTokens
	if s, ok := semantic[c.ID]; ok && s > sim {
		sim, reason = s, ReasonSemantic
	}
	if sim >= minSim {
		// Keep "similar" scores strictly below the "likely" tier.
		return Match{Candidate: c, Tier: TierSimilar, Score: round3(sim * 0.85), Reason: reason}, true
	}
	return Match{}, false
}

// TokenSimilarity is a Dice coefficient over token multisets.
func TokenSimilarity(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	counts := make(map[string]int, len(a))
	for _, t := range a {
		counts[t]++
	}
	inter := 0
	for _, t := range b {
		if counts[t] > 0 {
			counts[t]--
			inter++
		}
	}
	return 2 * float64(inter) / float64(len(a)+len(b))
}

func containsID(ids []string, want string) bool {
	for _, id := range ids {
		if NormalizeID(id) == want {
			return true
		}
	}
	return false
}

func tierRank(t Tier) int {
	switch t {
	case TierExact:
		return 0
	case TierLikely:
		return 1
	default:
		return 2
	}
}

func round3(f float64) float64 {
	return float64(int(f*1000+0.5)) / 1000
}

package dupcheck

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"ＵＳＢ－Ｃ　ケーブル", "usb c けーぶる"},
		{"USB-C ｹｰﾌﾞﾙ", "usb c けーぶる"},
		{"usb-c けーぶる", "usb c けーぶる"},
		{"  Anker  PowerCore, 10000mAh!! ", "anker powercore 10000mah"},
		{"【公式】ドライバー・セット", "公式 どらいばー せっと"},
		{"", ""},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, NormalizeName(c.in), c.in)
	}
}

func TestNormalizeID(t *testing.T) {
	assert.Equal(t, "B0ABC12345", NormalizeID("b0abc12345"))
	assert.Equal(t, "4901234567894", NormalizeID("４９０１２３４５６７８９４"))
	assert.Equal(t, "WH1000XM5", NormalizeID("WH-1000XM5"))
	assert.Empty(t, NormalizeID(" - "))
}

func TestTokensCJKBigrams(t *testing.T) {
	assert.Equal(t, []string{"usb", "けー", "ーぶ", "ぶる"}, Tokens("USBケーブル"))
	assert.Equal(t, []string{"anker", "10000mah"}, Tokens("Anker 10000mAh"))
}

func TestTokenSimilarity(t *testing.T) {
	assert.InDelta(t, 1.0, TokenSimilarity([]string{"a", "b"}, []string{"b", "a"}), 1e-9)
	assert.InDelta(t, 0.5, TokenSimilarity([]string{"a", "b"}, []string{"a", "c"}), 1e-9)
	assert.InDelta(t, 0.0, TokenSimilarity(nil, []string{"a"}), 1e-9)
}

func cand(name string, mod func(*Candidate)) Candidate {
	c := Candidate{ID: uuid.New(), Name: name}
	if mod != nil {
		mod(&c)
	}
	return c
}

func TestRankOrdering(t *testing.T) {
	asinHit := cand("Totally different name", func(c *Candidate) { c.ASINs = []string{"b0abc12345"} })
	janHit := cand("Other", func(c *Candidate) { c.JANs = []string{"4901234567894"} })
	modelHit := cand("Headphones", func(c *Candidate) { c.ModelNumbers = []string{"WH-1000XM5"} })
	nameHit := cand("ＳＯＮＹ ﾜｲﾔﾚｽ ヘッドホン", nil)
	similar := cand("Sony wireless headphones black", nil)
	unrelated := cand("Garden hose", nil)

	q := Query{Name: "Sony ワイヤレス ヘッドホン", ASIN: "B0ABC12345", JAN: "4901234567894", ModelNumber: "wh1000xm5"}
	ms, err := Rank(context.Background(), q,
		[]Candidate{unrelated, similar, nameHit, modelHit, janHit, asinHit}, Options{})
	require.NoError(t, err)

	got := make([]uuid.UUID, len(ms))
	for i, m := range ms {
		got[i] = m.Candidate.ID
	}
	// "similar" has only the brand in common with the Japanese query, so it is
	// below threshold; unrelated never matches.
	assert.Equal(t, []uuid.UUID{asinHit.ID, janHit.ID, modelHit.ID, nameHit.ID}, got)
	assert.Equal(t, ReasonASIN, ms[0].Reason)
	assert.Equal(t, ReasonJAN, ms[1].Reason)
	assert.Equal(t, ReasonModelNumber, ms[2].Reason)
	assert.Equal(t, TierExact, ms[2].Tier)
	assert.Equal(t, TierLikely, ms[3].Tier)
}

func TestRankSimilarAndLimit(t *testing.T) {
	a := cand("Anker PowerCore 10000 battery", nil)
	b := cand("Anker PowerCore 20000", nil)
	c := cand("Garden hose", nil)
	ms, err := Rank(context.Background(), Query{Name: "Anker PowerCore 10000"}, []Candidate{c, b, a}, Options{})
	require.NoError(t, err)
	require.Len(t, ms, 2)
	assert.Equal(t, a.ID, ms[0].Candidate.ID)
	assert.Equal(t, TierSimilar, ms[0].Tier)
	assert.Less(t, ms[0].Score, 0.9)
	assert.Greater(t, ms[0].Score, ms[1].Score)

	ms, err = Rank(context.Background(), Query{Name: "Anker PowerCore 10000"}, []Candidate{c, b, a}, Options{Limit: 1})
	require.NoError(t, err)
	assert.Len(t, ms, 1)
}

func TestRankEmptyQueryIDsDoNotMatchEmptyFields(t *testing.T) {
	ms, err := Rank(context.Background(), Query{Name: "x"}, []Candidate{cand("y", func(c *Candidate) { c.ASINs = []string{""} })}, Options{})
	require.NoError(t, err)
	assert.Empty(t, ms)
}

type fakeScorer map[uuid.UUID]float64

func (f fakeScorer) Score(context.Context, Query, []Candidate) (map[uuid.UUID]float64, error) {
	return f, nil
}

func TestRankSemanticHook(t *testing.T) {
	c := cand("Bluetooth earbuds", nil)
	ms, err := Rank(context.Background(), Query{Name: "ワイヤレスイヤホン"}, []Candidate{c},
		Options{Semantic: fakeScorer{c.ID: 0.8}})
	require.NoError(t, err)
	require.Len(t, ms, 1)
	assert.Equal(t, ReasonSemantic, ms[0].Reason)
	assert.Equal(t, TierSimilar, ms[0].Tier)
}

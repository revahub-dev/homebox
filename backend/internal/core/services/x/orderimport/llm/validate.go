package llm

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sysadminsmedia/homebox/backend/internal/core/services/x/orderimport/parsers"
)

type wireItem struct {
	Name        string  `json:"name"`
	Quantity    float64 `json:"quantity"`
	UnitPrice   float64 `json:"unit_price"`
	ASIN        string  `json:"asin"`
	JAN         string  `json:"jan"`
	ModelNumber string  `json:"model_number"`
}

type wireOrder struct {
	Store       string     `json:"store"`
	OrderNumber string     `json:"order_number"`
	OrderDate   string     `json:"order_date"`
	Total       float64    `json:"total"`
	Currency    string     `json:"currency"`
	Items       []wireItem `json:"items"`
}

// ErrInvalidOutput means the model returned something that fails the schema.
var ErrInvalidOutput = errors.New("llm returned invalid order JSON")

// Validate parses and sanitises model output. Strings are trimmed and length
// capped, numbers must be finite and non-negative, identifiers must look like
// identifiers, and items without names are dropped.
func Validate(content string) (*parsers.Order, error) {
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")

	var w wireOrder
	dec := json.NewDecoder(strings.NewReader(content))
	if err := dec.Decode(&w); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidOutput, err)
	}
	if !finite(w.Total) {
		return nil, fmt.Errorf("%w: bad total", ErrInvalidOutput)
	}
	o := &parsers.Order{
		Store:       capStr(w.Store, 32),
		OrderNumber: capStr(w.OrderNumber, 128),
		Total:       w.Total,
		Currency:    strings.ToUpper(capStr(w.Currency, 8)),
	}
	if o.Currency == "" {
		o.Currency = "JPY"
	}
	if t, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(w.OrderDate), time.FixedZone("JST", 9*3600)); err == nil {
		o.OrderDate = &t
	}
	for _, it := range w.Items {
		if len(o.Items) >= maxItems {
			break
		}
		name := capStr(it.Name, 500)
		if name == "" || !finite(it.Quantity) || !finite(it.UnitPrice) {
			continue
		}
		q := it.Quantity
		if q <= 0 {
			q = 1
		}
		o.Items = append(o.Items, parsers.LineItem{
			Name:        name,
			Quantity:    q,
			UnitPrice:   it.UnitPrice,
			ASIN:        identifier(it.ASIN, 10, 10),
			JAN:         digits(it.JAN),
			ModelNumber: capStr(it.ModelNumber, 255),
		})
	}
	return o, nil
}

func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) && f >= 0 }

func capStr(s string, n int) string {
	s = strings.TrimSpace(s)
	for utf8.RuneCountInString(s) > n {
		r := []rune(s)
		s = string(r[:n])
	}
	return s
}

func identifier(s string, minLen, maxLen int) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	if len(s) < minLen || len(s) > maxLen {
		return ""
	}
	for _, r := range s {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return ""
		}
	}
	return s
}

func digits(s string) string {
	s = strings.TrimSpace(s)
	if len(s) != 8 && len(s) != 13 {
		return ""
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return s
}

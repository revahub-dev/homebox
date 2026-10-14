// Package usebuild implements the fork's "use & build" features: idle-item
// nudges (weekly digest, keep/use/sell/archive) and "What can I build?".
package usebuild

// Fork addition. See FORK_NOTES.md.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/sysadminsmedia/homebox/backend/internal/core/services/x/aichat"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
)

const (
	// MaxPromptItems caps how many items are sent to the model.
	MaxPromptItems  = 300
	maxDescChars    = 120
	maxIdeas        = 5
	maxSteps        = 12
	maxMissingParts = 10

	StatusBuildable = "buildable_now"
	StatusMissing   = "missing_parts"
)

var (
	ErrEmptyInventory = errors.New("no items in scope")
	ErrBadModelReply  = errors.New("the model returned no usable ideas")
)

// OwnedPart is an owned item used by an idea. EntityID is always a real item
// of the collection: hallucinated references are dropped server-side.
type OwnedPart struct {
	EntityID uuid.UUID `json:"entityId"`
	Name     string    `json:"name"`
	Quantity float64   `json:"quantity"`
	Role     string    `json:"role"`
}

// Idea is one project suggestion.
type Idea struct {
	Title        string              `json:"title"`
	Description  string              `json:"description"`
	Status       string              `json:"status"       enums:"buildable_now,missing_parts"`
	Steps        []string            `json:"steps"`
	OwnedParts   []OwnedPart         `json:"ownedParts"`
	MissingParts []repo.XMissingPart `json:"missingParts"`
	// DroppedRefs counts owned-part references the model invented.
	DroppedRefs int `json:"droppedRefs"`
}

// BuildResult is the response of a build suggestion run.
type BuildResult struct {
	Ideas      []Idea `json:"ideas"`
	ItemsSent  int    `json:"itemsSent"`
	ItemsTotal int    `json:"itemsTotal"`
	Truncated  bool   `json:"truncated"`
}

const buildSystemPrompt = `You suggest small DIY / hobby / household projects that use items the user already owns.
You get the owned items as lines "ref | name | qty | description". Reply with one JSON object only:
{"ideas":[{"title":"","description":"","status":"buildable_now|missing_parts",
 "steps":["..."],
 "owned_parts":[{"ref":"i1","quantity":1,"role":"what it is used for"}],
 "missing_parts":[{"name":"","quantity":1,"note":""}]}]}
Rules: give 3 to 5 ideas. Use ONLY refs from the list for owned_parts; never invent refs.
Never list an owned item under missing_parts. "buildable_now" means missing_parts is empty;
otherwise use "missing_parts" and keep it to 1-3 cheap, common parts. Steps are short imperative sentences.
Reply in the same language as most item names.`

// Suggest asks the model for project ideas using the given items and
// validates the reply against them.
func Suggest(ctx context.Context, chat aichat.Chatter, items []repo.XInvItem, scope string) (BuildResult, error) {
	if chat == nil {
		return BuildResult{}, aichat.ErrDisabled
	}
	if len(items) == 0 {
		return BuildResult{}, ErrEmptyInventory
	}
	sel := append([]repo.XInvItem(nil), items...)
	// Sort by name for a stable prompt.
	sort.SliceStable(sel, func(i, j int) bool { return strings.ToLower(sel[i].Name) < strings.ToLower(sel[j].Name) })
	res := BuildResult{ItemsTotal: len(sel)}
	if len(sel) > MaxPromptItems {
		sel = sel[:MaxPromptItems]
		res.Truncated = true
	}
	res.ItemsSent = len(sel)

	refs := make(map[string]repo.XInvItem, len(sel))
	var b strings.Builder
	if scope != "" {
		b.WriteString("Scope: " + scope + "\n")
	}
	b.WriteString("Owned items:\n")
	for i, it := range sel {
		ref := "i" + strconv.Itoa(i+1)
		refs[ref] = it
		refs[it.ID.String()] = it // accept full IDs too
		avail := it.Quantity - it.ReservedQuantity
		if avail < 0 {
			avail = 0
		}
		fmt.Fprintf(&b, "%s | %s | %s | %s\n", ref, oneLine(it.Name, 200), trimFloat(avail), oneLine(it.Description, maxDescChars))
	}

	out, err := chat.Chat(ctx, buildSystemPrompt, b.String(), true)
	if err != nil {
		return BuildResult{}, err
	}
	ideas, err := parseIdeas(out, refs)
	if err != nil {
		return BuildResult{}, err
	}
	res.Ideas = ideas
	return res, nil
}

type rawIdea struct {
	Title        string          `json:"title"`
	Description  string          `json:"description"`
	Status       string          `json:"status"`
	Steps        []string        `json:"steps"`
	OwnedParts   []rawOwned      `json:"owned_parts"`
	MissingParts json.RawMessage `json:"missing_parts"`
}

type rawOwned struct {
	Ref      string  `json:"ref"`
	ID       string  `json:"id"`
	Quantity float64 `json:"quantity"`
	Role     string  `json:"role"`
}

// parseIdeas validates the model output: unknown refs are dropped,
// quantities are clamped to what is owned, and the status is recomputed
// from the missing parts.
func parseIdeas(reply string, refs map[string]repo.XInvItem) ([]Idea, error) {
	js := aichat.ExtractJSON(reply)
	if js == "" {
		return nil, ErrBadModelReply
	}
	var raw struct {
		Ideas []rawIdea `json:"ideas"`
	}
	if err := json.Unmarshal([]byte(js), &raw); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrBadModelReply, err)
	}
	out := make([]Idea, 0, maxIdeas)
	for _, ri := range raw.Ideas {
		title := oneLine(ri.Title, 200)
		if title == "" {
			continue
		}
		idea := Idea{
			Title: title, Description: strings.TrimSpace(ri.Description),
			Steps: []string{}, OwnedParts: []OwnedPart{}, MissingParts: parseMissing(ri.MissingParts),
		}
		for _, s := range ri.Steps {
			if s = strings.TrimSpace(s); s != "" && len(idea.Steps) < maxSteps {
				idea.Steps = append(idea.Steps, s)
			}
		}
		seen := map[uuid.UUID]int{}
		for _, op := range ri.OwnedParts {
			key := strings.TrimSpace(op.Ref)
			if key == "" {
				key = strings.TrimSpace(op.ID)
			}
			it, ok := refs[strings.ToLower(key)]
			if !ok {
				it, ok = refs[key]
			}
			if !ok {
				idea.DroppedRefs++
				continue
			}
			q := op.Quantity
			if q <= 0 {
				q = 1
			}
			if it.Quantity > 0 && q > it.Quantity {
				q = it.Quantity
			}
			if i, dup := seen[it.ID]; dup {
				idea.OwnedParts[i].Quantity = math.Min(idea.OwnedParts[i].Quantity+q, math.Max(it.Quantity, 1))
				continue
			}
			seen[it.ID] = len(idea.OwnedParts)
			idea.OwnedParts = append(idea.OwnedParts, OwnedPart{EntityID: it.ID, Name: it.Name, Quantity: q, Role: oneLine(op.Role, 200)})
		}
		if len(idea.OwnedParts) == 0 {
			continue // an idea must use something the user owns
		}
		if len(idea.MissingParts) == 0 {
			idea.Status = StatusBuildable
		} else {
			idea.Status = StatusMissing
		}
		out = append(out, idea)
		if len(out) == maxIdeas {
			break
		}
	}
	if len(out) == 0 {
		return nil, ErrBadModelReply
	}
	return out, nil
}

func parseMissing(raw json.RawMessage) []repo.XMissingPart {
	out := []repo.XMissingPart{}
	if len(raw) == 0 {
		return out
	}
	var objs []struct {
		Name     string  `json:"name"`
		Quantity float64 `json:"quantity"`
		Note     string  `json:"note"`
	}
	if json.Unmarshal(raw, &objs) != nil {
		var names []string
		if json.Unmarshal(raw, &names) != nil {
			return out
		}
		for _, n := range names {
			objs = append(objs, struct {
				Name     string  `json:"name"`
				Quantity float64 `json:"quantity"`
				Note     string  `json:"note"`
			}{Name: n})
		}
	}
	for _, o := range objs {
		n := oneLine(o.Name, 200)
		if n == "" || len(out) >= maxMissingParts {
			continue
		}
		q := o.Quantity
		if q <= 0 {
			q = 1
		}
		out = append(out, repo.XMissingPart{Name: n, Quantity: q, Note: oneLine(o.Note, 300)})
	}
	return out
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(strings.ReplaceAll(s, "|", "/")), " ")
	if r := []rune(s); len(r) > max {
		s = string(r[:max]) + "…"
	}
	return s
}

func trimFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

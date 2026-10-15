package vision

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"golang.org/x/text/unicode/norm"

	"github.com/sysadminsmedia/homebox/backend/internal/core/services/x/aichat"
)

const (
	// MaxDetections caps rows kept per photo.
	MaxDetections = 40
	maxNameRunes  = 255
	maxFieldRunes = 255

	minBoxArea = 0.002 // < 0.2% of the image
	maxBoxArea = 0.95
	nmsIoU     = 0.6
)

// genericNames are too vague to trust at full confidence.
var genericNames = map[string]struct{}{
	"item": {}, "items": {}, "object": {}, "objects": {}, "thing": {}, "things": {}, "stuff": {},
	"box": {}, "bag": {}, "もの": {}, "物": {}, "アイテム": {}, "商品": {},
}

type rawReply struct {
	Items []rawItem `json:"items"`
}

type rawItem struct {
	Name        string          `json:"name"`
	Brand       string          `json:"brand"`
	ModelNumber string          `json:"model_number"`
	Category    string          `json:"category"`
	Quantity    json.RawMessage `json:"quantity"`
	Confidence  json.RawMessage `json:"confidence"`
	BBox        json.RawMessage `json:"bbox"`
}

// ParseReply validates a model reply. w and h are the pixel size of the image
// that was sent (used when a model answers in pixels).
func ParseReply(reply string, mode Mode, w, h int) (DetectResult, error) {
	var rr rawReply
	js := aichat.ExtractJSON(reply)
	objOK := js != "" && json.Unmarshal([]byte(js), &rr) == nil
	if !objOK || rr.Items == nil {
		// some models return a bare array
		if start, end := strings.Index(reply, "["), strings.LastIndex(reply, "]"); start >= 0 && end > start {
			var items []rawItem
			if json.Unmarshal([]byte(reply[start:end+1]), &items) == nil && len(items) > 0 {
				rr.Items, objOK = items, true
			}
		}
	}
	if !objOK {
		return DetectResult{}, errors.New("vision: unparseable model reply")
	}

	res := DetectResult{Detections: []Detection{}}
	for _, it := range rr.Items {
		d, ok := normalise(it, mode, w, h)
		if !ok {
			res.Dropped++
			continue
		}
		res.Detections = append(res.Detections, d)
	}
	res.Detections, res.Dropped = nms(res.Detections, res.Dropped)
	sort.SliceStable(res.Detections, func(i, j int) bool { return res.Detections[i].Confidence > res.Detections[j].Confidence })
	if len(res.Detections) > MaxDetections {
		res.Dropped += len(res.Detections) - MaxDetections
		res.Detections = res.Detections[:MaxDetections]
	}
	return res, nil
}

func normalise(it rawItem, mode Mode, w, h int) (Detection, bool) {
	name := clean(it.Name, maxNameRunes)
	if name == "" {
		return Detection{}, false
	}
	d := Detection{
		Name:        name,
		Brand:       clean(it.Brand, maxFieldRunes),
		ModelNumber: clean(it.ModelNumber, maxFieldRunes),
		Category:    clean(it.Category, 64),
		Quantity:    clampQty(number(it.Quantity, 1)),
		Confidence:  clamp01(number(it.Confidence, 0.5)),
		BBox:        parseBBox(it.BBox, w, h),
	}
	// Calibration: models over-report. Penalise what we can't verify.
	d.Confidence = math.Min(d.Confidence, 0.95)
	if mode == ModeShelf && d.BBox == nil {
		d.Confidence *= 0.7
	}
	if _, generic := genericNames[strings.ToLower(d.Name)]; generic || len([]rune(d.Name)) < 3 {
		d.Confidence *= 0.8
	}
	d.Confidence = math.Round(d.Confidence*100) / 100
	return d, true
}

func clean(s string, max int) string {
	s = norm.NFKC.String(strings.Join(strings.Fields(s), " "))
	if r := []rune(s); len(r) > max {
		s = string(r[:max])
	}
	return s
}

func clamp01(f float64) float64 {
	switch {
	case math.IsNaN(f), f < 0:
		return 0
	case f > 1:
		// Some models answer 0-100.
		if f <= 100 {
			return f / 100
		}
		return 1
	}
	return f
}

func clampQty(f float64) float64 {
	if math.IsNaN(f) || f < 1 {
		return 1
	}
	if f > 999 {
		return 999
	}
	return math.Round(f)
}

// number accepts JSON numbers and numeric strings.
func number(raw json.RawMessage, def float64) float64 {
	if len(raw) == 0 {
		return def
	}
	var f float64
	if json.Unmarshal(raw, &f) == nil {
		return f
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if _, err := fmt.Sscanf(strings.TrimSpace(s), "%g", &f); err == nil {
			return f
		}
	}
	return def
}

// parseBBox understands [x1,y1,x2,y2] as fractions (0..1), per-mille
// (0..1000, what the prompt asks for) or pixels of the sent image. Returns nil
// for anything unusable.
func parseBBox(raw json.RawMessage, w, h int) *BBox {
	if len(raw) == 0 {
		return nil
	}
	var v []float64
	if json.Unmarshal(raw, &v) != nil || len(v) != 4 {
		return nil
	}
	max := 0.0
	for _, f := range v {
		if math.IsNaN(f) || math.IsInf(f, 0) || f < 0 {
			return nil
		}
		max = math.Max(max, f)
	}
	var x1, y1, x2, y2 float64
	switch {
	case max <= 1.0001:
		x1, y1, x2, y2 = v[0], v[1], v[2], v[3]
	case max <= 1000:
		x1, y1, x2, y2 = v[0]/1000, v[1]/1000, v[2]/1000, v[3]/1000
	case w > 0 && h > 0:
		x1, y1, x2, y2 = v[0]/float64(w), v[1]/float64(h), v[2]/float64(w), v[3]/float64(h)
	default:
		return nil
	}
	if x2 < x1 {
		x1, x2 = x2, x1
	}
	if y2 < y1 {
		y1, y2 = y2, y1
	}
	x1, y1, x2, y2 = math.Min(x1, 1), math.Min(y1, 1), math.Min(x2, 1), math.Min(y2, 1)
	b := &BBox{X: x1, Y: y1, W: x2 - x1, H: y2 - y1}
	if a := b.W * b.H; a < minBoxArea || a > maxBoxArea {
		return nil
	}
	return b
}

func iou(a, b BBox) float64 {
	x1, y1 := math.Max(a.X, b.X), math.Max(a.Y, b.Y)
	x2, y2 := math.Min(a.X+a.W, b.X+b.W), math.Min(a.Y+a.H, b.Y+b.H)
	if x2 <= x1 || y2 <= y1 {
		return 0
	}
	inter := (x2 - x1) * (y2 - y1)
	return inter / (a.W*a.H + b.W*b.H - inter)
}

// nms drops same-name detections whose boxes overlap heavily, keeping the
// most confident one, and merges exact duplicate names without boxes.
func nms(ds []Detection, dropped int) ([]Detection, int) {
	sort.SliceStable(ds, func(i, j int) bool { return ds[i].Confidence > ds[j].Confidence })
	out := make([]Detection, 0, len(ds))
	for _, d := range ds {
		dup := false
		for _, k := range out {
			if !strings.EqualFold(k.Name, d.Name) {
				continue
			}
			if d.BBox != nil && k.BBox != nil && iou(*d.BBox, *k.BBox) > nmsIoU {
				dup = true
			}
			if d.BBox == nil && k.BBox == nil {
				dup = true
			}
			if dup {
				break
			}
		}
		if dup {
			dropped++
			continue
		}
		out = append(out, d)
	}
	return out, dropped
}

package vision

import "strings"

const systemPrompt = `You catalogue household belongings from photos for a home inventory.
Reply with ONE JSON object and nothing else, exactly this shape:
{"items":[{"name":"","brand":"","model_number":"","category":"","quantity":1,"confidence":0.0,"bbox":[0,0,0,0]}]}
Rules:
- One entry per distinct kind of item. Identical items together are ONE entry with quantity.
- name: short and specific ("Anker 20W USB-C charger", not "charger"). Keep Japanese or other text exactly as printed on the item.
- brand and model_number: ONLY when legibly printed. Otherwise "". Never guess or invent them.
- category: one or two plain words ("kitchen", "cable", "tool").
- confidence: 0 to 1, how sure you are of the name. Use below 0.5 when unsure.
- bbox: [x_min, y_min, x_max, y_max] of the item, each 0 to 1000 relative to the image width and height (0,0 = top-left). Use [0,0,0,0] if you cannot locate it.
- Ignore people, pets, walls, floors, furniture, and anything that is not an owned object.
- If nothing can be identified, return {"items":[]}.`

func userPrompt(req DetectRequest) string {
	var b strings.Builder
	switch req.Mode {
	case ModeShelf:
		b.WriteString("This photo shows a shelf, drawer, box or table with several items. List every individually distinguishable item you can see (at most 40), most prominent first.")
	default:
		b.WriteString("This photo shows one main item, possibly with its packaging or label. List that item. Add other items only if they are clearly separate objects of interest.")
	}
	if req.Language != "" {
		b.WriteString(" Write generic descriptions in the language with code \"" + sanitizeLang(req.Language) + "\".")
	}
	return b.String()
}

func sanitizeLang(l string) string {
	var b strings.Builder
	for _, r := range l {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '-' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 || b.Len() > 12 {
		return "en"
	}
	return b.String()
}

package vision

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sysadminsmedia/homebox/backend/internal/core/services/x/aichat"
)

func TestParseReplyBoxesAndCalibration(t *testing.T) {
	reply := "Here you go:\n```json\n" + `{"items":[
	 {"name":"Anker 20W 充電器","brand":"Anker","model_number":"A2633","category":"cable","quantity":"2","confidence":0.9,"bbox":[100,200,400,600]},
	 {"name":"Drill","quantity":1,"confidence":0.8,"bbox":[0.1,0.1,0.5,0.5]},
	 {"name":"Tape","confidence":90,"bbox":[200,100,1000,500]},
	 {"name":"Tape","confidence":0.4,"bbox":[210,110,1000,500]},
	 {"name":"thing","confidence":0.9},
	 {"name":"","confidence":0.9},
	 {"name":"Weird box","confidence":0.7,"bbox":[0,0,0,0]},
	 {"name":"Full","confidence":0.7,"bbox":[0,0,1000,1000]}
	]}` + "\n```"
	res, err := ParseReply(reply, ModeShelf, 1600, 1200)
	require.NoError(t, err)

	byName := map[string]Detection{}
	for _, d := range res.Detections {
		byName[d.Name] = d
	}
	a := byName["Anker 20W 充電器"]
	assert.InDelta(t, 2.0, a.Quantity, 1e-9, "numeric string accepted")
	require.NotNil(t, a.BBox)
	assert.InDelta(t, 0.1, a.BBox.X, 1e-9)
	assert.InDelta(t, 0.3, a.BBox.W, 1e-9, "per-mille box")
	assert.InDelta(t, 0.9, a.Confidence, 1e-9)

	dr := byName["Drill"]
	require.NotNil(t, dr.BBox)
	assert.InDelta(t, 0.4, dr.BBox.W, 1e-9, "fractional box")

	tp := byName["Tape"]
	assert.InDelta(t, 0.9*1.0, tp.Confidence/1.0, 0.06, "0-100 confidence scaled and capped at .95")
	assert.Len(t, res.Detections, 6, "empty name and the overlapping duplicate Tape are dropped")

	assert.Less(t, byName["thing"].Confidence, 0.9*0.7, "generic + no box in shelf mode is penalised")
	assert.Nil(t, byName["Weird box"].BBox, "zero box → nil")
	assert.Nil(t, byName["Full"].BBox, "box covering the whole image is rejected")
	assert.Equal(t, 2, res.Dropped)
	assert.GreaterOrEqual(t, res.Detections[0].Confidence, res.Detections[len(res.Detections)-1].Confidence, "sorted")
}

func TestParseBBoxPixelsAndErrors(t *testing.T) {
	b := parseBBox(json.RawMessage(`[160,120,800,600]`), 1600, 1200)
	require.NotNil(t, b)
	assert.InDelta(t, 0.16, b.X, 1e-9) // ≤1000 is per-mille even if the image is bigger
	b = parseBBox(json.RawMessage(`[1600,1200,3200,2400]`), 3200, 2400)
	require.NotNil(t, b, "pixel box")
	assert.InDelta(t, 0.5, b.X, 1e-9)
	assert.Nil(t, parseBBox(json.RawMessage(`[1,2,3]`), 10, 10))
	assert.Nil(t, parseBBox(json.RawMessage(`[-1,0,5,5]`), 10, 10))
	assert.Nil(t, parseBBox(json.RawMessage(`"x"`), 10, 10))
	assert.Nil(t, parseBBox(json.RawMessage(`[500,500,500.5,500.5]`), 1000, 1000), "tiny")

	_, err := ParseReply("sorry I cannot", ModeItem, 10, 10)
	require.Error(t, err)
	res, err := ParseReply(`{"items":[]}`, ModeItem, 10, 10)
	require.NoError(t, err)
	assert.Empty(t, res.Detections)
	res, err = ParseReply(`[{"name":"Mug","confidence":0.8}]`, ModeItem, 10, 10)
	require.NoError(t, err)
	require.Len(t, res.Detections, 1)

	// caps at MaxDetections
	var rows []map[string]any
	for i := 0; i < 60; i++ {
		rows = append(rows, map[string]any{"name": "Item " + string(rune('A'+i%26)) + string(rune('a'+i/26)), "confidence": 0.8})
	}
	js, _ := json.Marshal(map[string]any{"items": rows})
	res, err = ParseReply(string(js), ModeShelf, 10, 10)
	require.NoError(t, err)
	assert.Len(t, res.Detections, MaxDetections)
}

func testJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 255 / w), uint8(y * 255 / h), 120, 255})
		}
	}
	var buf bytes.Buffer
	require.NoError(t, jpeg.Encode(&buf, img, nil))
	return buf.Bytes()
}

func TestNormaliseAndCrop(t *testing.T) {
	big := testJPEG(t, 3200, 2400)
	out, w, h, err := Normalise(big)
	require.NoError(t, err)
	assert.Equal(t, 1600, w)
	assert.Equal(t, 1200, h)
	assert.Less(t, len(out), len(big)+1)

	img, err := Decode(out)
	require.NoError(t, err)
	crop, err := Crop(img, BBox{X: 0.1, Y: 0.1, W: 0.5, H: 0.4})
	require.NoError(t, err)
	cfg, _, err := image.DecodeConfig(bytes.NewReader(crop))
	require.NoError(t, err)
	assert.LessOrEqual(t, cfg.Width, CropMaxEdge)
	assert.Equal(t, CropMaxEdge, cfg.Width)

	_, err = Crop(img, BBox{X: 0.999, Y: 0.999, W: 0.0005, H: 0.0005})
	require.Error(t, err)

	bad, w, h, err := Normalise([]byte("not an image"))
	require.Error(t, err)
	assert.Nil(t, bad)
	assert.Zero(t, w+h)
}

func TestOpenAIDetectAgainstFakeServer(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		reply, _ := json.Marshal(map[string]any{"choices": []map[string]any{{"message": map[string]string{
			"content": `{"items":[{"name":"Kettle","confidence":0.85,"bbox":[100,100,500,600]}]}`}}}})
		_, _ = w.Write(reply)
	}))
	defer srv.Close()

	chat := aichat.New(aichat.Config{BaseURL: srv.URL, Model: "text", VisionModel: "vlm"})
	p := NewOpenAI(chat, "")
	require.NotNil(t, p)
	assert.Equal(t, "openai-compatible", p.Name())
	res, err := p.Detect(context.Background(), DetectRequest{Image: testJPEG(t, 64, 64), Mode: ModeShelf, Width: 64, Height: 64, Language: "ja"})
	require.NoError(t, err)
	require.Len(t, res.Detections, 1)
	assert.Equal(t, "Kettle", res.Detections[0].Name)
	assert.Equal(t, "vlm", got["model"])
	msgs := got["messages"].([]any)
	user := msgs[1].(map[string]any)["content"].([]any)
	assert.Contains(t, user[0].(map[string]any)["text"], "shelf")
	assert.Contains(t, user[0].(map[string]any)["text"], `"ja"`)
	assert.Contains(t, user[1].(map[string]any)["image_url"].(map[string]any)["url"], "data:image/jpeg;base64,")

	_, err = p.Detect(context.Background(), DetectRequest{})
	require.Error(t, err)
	assert.Nil(t, NewOpenAI(nil, ""))
}

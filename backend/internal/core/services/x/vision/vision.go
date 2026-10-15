// Package vision turns photos into inventory detections. It defines a
// Provider interface and ships an OpenAI-compatible implementation that works
// with cloud APIs and local servers (Ollama, LM Studio). Everything a model
// returns is validated and clamped here before it reaches the database.
package vision

// Fork addition (photo-first capture). See FORK_NOTES.md.

import (
	"context"
	"errors"
)

// Mode is what the user is photographing.
type Mode string

const (
	// ModeItem is one dominant item (possibly with its box or label).
	ModeItem Mode = "item"
	// ModeShelf is a shelf, drawer or table with many items.
	ModeShelf Mode = "shelf"
)

// Valid reports whether m is a supported capture mode.
func (m Mode) Valid() bool { return m == ModeItem || m == ModeShelf }

// ErrDisabled is returned when no vision provider is configured.
var ErrDisabled = errors.New("vision: no AI endpoint configured")

// BBox is a bounding box normalised to 0..1 of the image, top-left origin.
type BBox struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

// Detection is one item found in a photo.
type Detection struct {
	Name        string  `json:"name"`
	Brand       string  `json:"brand"`
	ModelNumber string  `json:"modelNumber"`
	Category    string  `json:"category"`
	Quantity    float64 `json:"quantity"`
	// Confidence is 0..1 after calibration (models over-report).
	Confidence float64 `json:"confidence"`
	// BBox is nil when the model gave no usable box.
	BBox *BBox `json:"bbox,omitempty"`
}

// DetectRequest is one photo to analyse.
type DetectRequest struct {
	// Image is a JPEG/PNG/WebP, already resized by the client.
	Image []byte
	MIME  string
	Mode  Mode
	// Width and Height of Image in pixels (used to interpret pixel boxes).
	Width, Height int
	// Language is a hint for names ("ja", "en"); original text on packaging
	// is always kept as written.
	Language string
}

// DetectResult is the validated outcome for one photo.
type DetectResult struct {
	Detections []Detection `json:"detections"`
	// Dropped counts model rows rejected by validation.
	Dropped int `json:"dropped"`
}

// Provider analyses a photo. Implementations must be safe for concurrent use.
type Provider interface {
	Detect(ctx context.Context, req DetectRequest) (DetectResult, error)
	// Name identifies the backend for logs and the status endpoint.
	Name() string
}

// Package arboocr is a Go wrapper for arboOCR (https://github.com/wafik/ArboOCR) —
// runs the prebuilt arboocr_demo binary via os/exec, no C++ build required.
package arboocr

// Point is one vertex of a LineResult's polygon.
type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// LineResult is one recognized text line. Polygon points are in the order
// arboOCR reports them (clockwise from top-left-ish).
type LineResult struct {
	Text     string  `json:"text"`
	Score    float64 `json:"score"`
	DetScore float64 `json:"detScore"`
	Polygon  []Point `json:"polygon"`
}

// PageResult is a full-page OCR result — mirrors arboOCR's PagePrediction.
// An empty Lines slice is a normal, successful result (no text found), not
// an error.
type PageResult struct {
	Backend   string       `json:"backend"`
	Image     string       `json:"image"`
	ElapsedMs float64      `json:"elapsedMs"`
	Lines     []LineResult `json:"lines"`
}

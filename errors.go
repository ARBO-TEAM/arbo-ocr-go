package arboocr

import "fmt"

// OcrError is returned only when the arboocr_demo process itself fails to
// start, exits non-zero, or produces unparseable output. An empty
// PageResult.Lines is a normal, successful result — not an error.
type OcrError struct {
	Message  string
	ExitCode int
	Stderr   string
}

func (e *OcrError) Error() string {
	if e.Stderr != "" {
		return fmt.Sprintf("%s: %s", e.Message, e.Stderr)
	}
	return e.Message
}

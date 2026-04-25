package output

import (
	"io"
	"os"
)

// Write dispatches output to the correct format based on the format flag.
func Write(w io.Writer, format string, v any) error {
	switch format {
	case "json":
		return WriteJSON(w, v)
	default:
		return WriteJSON(w, v) // fallback to JSON for structured data
	}
}

// Stdout returns a convenience reference to os.Stdout.
func Stdout() io.Writer {
	return os.Stdout
}

// Stderr returns a convenience reference to os.Stderr.
func Stderr() io.Writer {
	return os.Stderr
}

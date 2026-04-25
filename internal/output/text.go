package output

import (
	"fmt"
	"io"
)

// WriteText writes a key-value pair in human-readable text format.
func WriteText(w io.Writer, key string, value any) {
	fmt.Fprintf(w, "%-20s %v\n", key+":", value)
}

// WriteHeader writes a section header line.
func WriteHeader(w io.Writer, title string) {
	fmt.Fprintf(w, "=== %s ===\n", title)
}

// WriteSuccess writes a success message.
func WriteSuccess(w io.Writer, msg string) {
	fmt.Fprintf(w, "✓ %s\n", msg)
}

// WriteWarning writes a warning message.
func WriteWarning(w io.Writer, msg string) {
	fmt.Fprintf(w, "⚠ %s\n", msg)
}

package output

import (
	"encoding/json"
	"fmt"
	"io"
)

// WriteJSON writes the value as indented JSON to the writer.
func WriteJSON(w io.Writer, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("json marshal: %w", err)
	}
	_, err = fmt.Fprintln(w, string(data))
	return err
}

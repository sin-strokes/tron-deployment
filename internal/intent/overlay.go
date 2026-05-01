package intent

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// LoadWithOverlay loads a base intent and merges an overlay on top of it.
// The overlay can override any field; arrays (nodes) are replaced entirely.
func LoadWithOverlay(basePath, overlayPath string) (*Intent, error) {
	base, err := Load(basePath)
	if err != nil {
		return nil, fmt.Errorf("load base intent: %w", err)
	}

	overlayData, err := os.ReadFile(overlayPath)
	if err != nil {
		return nil, fmt.Errorf("read overlay file: %w", err)
	}

	if err := mergeOverlay(base, overlayData); err != nil {
		return nil, fmt.Errorf("apply overlay: %w", err)
	}

	// Re-validate after merge
	if err := Validate(base); err != nil {
		return nil, fmt.Errorf("validate after overlay: %w", err)
	}

	ApplyDefaults(base)
	return base, nil
}

// mergeOverlay applies overlay YAML on top of the base intent.
// Strategy: unmarshal overlay into a map, then re-marshal and unmarshal onto the base.
func mergeOverlay(base *Intent, overlayData []byte) error {
	// Parse overlay as a partial intent
	var overlay Intent
	if err := yaml.Unmarshal(overlayData, &overlay); err != nil {
		return fmt.Errorf("parse overlay YAML: %w", err)
	}

	// Simple field-level merge: override non-zero values
	if overlay.Name != "" {
		base.Name = overlay.Name
	}
	if overlay.Network != "" {
		base.Network = overlay.Network
	}
	if overlay.Target.Type != "" {
		base.Target = overlay.Target
	}
	if len(overlay.Nodes) > 0 {
		base.Nodes = overlay.Nodes
	}

	return nil
}

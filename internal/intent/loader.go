package intent

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/go-playground/validator/v10"
	"gopkg.in/yaml.v3"
)

var validate = validator.New()

// namePattern enforces DNS-label-safe names: ^[a-z0-9][a-z0-9-]{0,62}$
var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// hexPattern detects raw hex keys accidentally placed in witness_key_env.
var hexPattern = regexp.MustCompile(`^[0-9a-fA-F]{32,}$`)

// Load reads an intent YAML file, validates it, applies defaults, and returns the parsed Intent.
func Load(path string) (*Intent, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read intent file: %w", err)
	}

	return Parse(data)
}

// Parse parses intent YAML bytes, validates, and applies defaults.
func Parse(data []byte) (*Intent, error) {
	var intent Intent
	if err := yaml.Unmarshal(data, &intent); err != nil {
		return nil, fmt.Errorf("parse intent YAML: %w", err)
	}

	if err := Validate(&intent); err != nil {
		return nil, err
	}

	ApplyDefaults(&intent)
	return &intent, nil
}

// Validate checks the intent against schema rules and security constraints.
func Validate(intent *Intent) error {
	// Name format
	if !namePattern.MatchString(intent.Name) {
		return fmt.Errorf("invalid name %q: must match ^[a-z0-9][a-z0-9-]{0,62}$", intent.Name)
	}

	// Struct validation via tags
	if err := validate.Struct(intent); err != nil {
		return fmt.Errorf("intent validation: %w", err)
	}

	// Witness key env must be a variable name, not a raw key
	for i, node := range intent.Nodes {
		if node.Type == "witness" {
			if node.WitnessKeyEnv == "" {
				return fmt.Errorf("nodes[%d]: witness node requires witness_key_env", i)
			}
			if err := validateWitnessKeyEnv(node.WitnessKeyEnv); err != nil {
				return fmt.Errorf("nodes[%d]: %w", i, err)
			}
		}
	}

	return nil
}

// validateWitnessKeyEnv ensures the value is an env var name, not a raw private key.
func validateWitnessKeyEnv(value string) error {
	// Reject if it looks like a hex key
	if hexPattern.MatchString(value) {
		return fmt.Errorf("witness_key_env %q looks like a raw private key; it must be an environment variable NAME (e.g., SR_PRIVATE_KEY)", value)
	}

	// Reject if it starts with 0x (hex prefix)
	if strings.HasPrefix(value, "0x") || strings.HasPrefix(value, "0X") {
		return fmt.Errorf("witness_key_env %q looks like a raw private key; it must be an environment variable NAME", value)
	}

	// Reject if it contains characters not valid in env var names
	envVarPattern := regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	if !envVarPattern.MatchString(value) {
		return fmt.Errorf("witness_key_env %q is not a valid environment variable name", value)
	}

	return nil
}

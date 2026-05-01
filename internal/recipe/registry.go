package recipe

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed files/*.yaml
var recipeFS embed.FS

// LoadEmbedded returns every recipe shipped inside the binary, indexed
// by .Name (NOT by filename — the YAML's name field is authoritative).
func LoadEmbedded() (map[string]Recipe, error) {
	out := map[string]Recipe{}
	entries, err := fs.ReadDir(recipeFS, "files")
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := fs.ReadFile(recipeFS, "files/"+e.Name())
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		var r Recipe
		if err := yaml.Unmarshal(data, &r); err != nil {
			return nil, fmt.Errorf("parse %s: %w", e.Name(), err)
		}
		if r.Name == "" {
			return nil, fmt.Errorf("recipe %s has no .name field", e.Name())
		}
		if _, dup := out[r.Name]; dup {
			return nil, fmt.Errorf("duplicate recipe name %q (in %s)", r.Name, e.Name())
		}
		out[r.Name] = r
	}
	return out, nil
}

// Names returns the embedded recipe names sorted lexicographically.
func Names() ([]string, error) {
	all, err := LoadEmbedded()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(all))
	for k := range all {
		names = append(names, k)
	}
	sort.Strings(names)
	return names, nil
}

// Get returns one recipe by name.
func Get(name string) (Recipe, error) {
	all, err := LoadEmbedded()
	if err != nil {
		return Recipe{}, err
	}
	r, ok := all[name]
	if !ok {
		return Recipe{}, fmt.Errorf("recipe %q not found; available: %s", name, strings.Join(sortedNames(all), ", "))
	}
	return r, nil
}

func sortedNames(m map[string]Recipe) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

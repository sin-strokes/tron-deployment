package knowledge

import (
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
)

//go:embed files/*.md
var knowledgeFS embed.FS

// Topics returns the list of available knowledge topics.
func Topics() []string {
	var topics []string
	fs.WalkDir(knowledgeFS, "files", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name := strings.TrimSuffix(filepath.Base(path), ".md")
		topics = append(topics, name)
		return nil
	})
	return topics
}

// Get returns the content of a knowledge topic.
func Get(topic string) (string, error) {
	data, err := knowledgeFS.ReadFile("files/" + topic + ".md")
	if err != nil {
		return "", fmt.Errorf("topic %q not found; available topics: %s", topic, strings.Join(Topics(), ", "))
	}
	return string(data), nil
}

// Package skillconfig validates the privacy-safe metadata envelope of the open
// Agent Skills SKILL.md format. It never returns or retains the instruction
// body or description.
package skillconfig

import (
	"strings"

	"gopkg.in/yaml.v3"
)

type Metadata struct {
	Name  string
	Valid bool
}

func Parse(data []byte, directory string) Metadata {
	fallback := strings.TrimSpace(directory)
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return Metadata{Name: fallback}
	}
	end := strings.Index(text[4:], "\n---")
	if end < 0 {
		return Metadata{Name: fallback}
	}
	var metadata map[string]any
	if err := yaml.Unmarshal([]byte(text[4:4+end]), &metadata); err != nil {
		return Metadata{Name: fallback}
	}
	name, _ := metadata["name"].(string)
	description, _ := metadata["description"].(string)
	name = strings.TrimSpace(name)
	validName := validSkillName(name)
	validDescription := strings.TrimSpace(description) != "" && len(description) <= 1024
	return Metadata{Name: chooseName(name, fallback, validName), Valid: validName && validDescription && strings.EqualFold(name, directory)}
}

func chooseName(name, fallback string, valid bool) string {
	if valid {
		return name
	}
	return fallback
}

func validSkillName(value string) bool {
	if value == "" || len(value) > 64 || value[0] == '-' || value[len(value)-1] == '-' {
		return false
	}
	previousDash := false
	for _, character := range value {
		if character == '-' {
			if previousDash {
				return false
			}
			previousDash = true
			continue
		}
		previousDash = false
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') {
			return false
		}
	}
	return true
}

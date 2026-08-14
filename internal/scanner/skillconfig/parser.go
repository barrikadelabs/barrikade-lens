// Package skillconfig validates the privacy-safe metadata envelope of the open
// Agent Skills SKILL.md format. It never returns or retains the instruction
// body. Bounded declarative frontmatter is retained for inventory.
package skillconfig

import (
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type Metadata struct {
	Name                  string
	Valid                 bool
	DescriptionPresent    bool
	LicenseDeclared       bool
	CompatibilityDeclared bool
	AllowedToolsDeclared  bool
	DeclaredPurpose       string
	License               string
	Compatibility         string
	AllowedTools          []string
	DescriptorFields      []string
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
	return Metadata{
		Name: chooseName(name, fallback, validName), Valid: validName && validDescription && strings.EqualFold(name, directory),
		DescriptionPresent: strings.TrimSpace(description) != "",
		LicenseDeclared:    nonEmptyString(metadata["license"]), CompatibilityDeclared: nonEmptyString(metadata["compatibility"]),
		AllowedToolsDeclared: declaredListOrString(metadata["allowed-tools"]), DeclaredPurpose: boundedMetadataText(description, 1000),
		License: boundedMetadataText(stringValue(metadata["license"]), 256), Compatibility: boundedMetadataText(stringValue(metadata["compatibility"]), 500),
		AllowedTools: safeAllowedTools(metadata["allowed-tools"]), DescriptorFields: safeDescriptorFields(metadata),
	}
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func boundedMetadataText(value string, limit int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	runes := []rune(value)
	if len(runes) > limit {
		value = string(runes[:limit])
	}
	return value
}

func safeAllowedTools(value any) []string {
	candidates := []string{}
	switch typed := value.(type) {
	case string:
		candidates = strings.FieldsFunc(typed, func(character rune) bool {
			return character == ',' || character == ';' || character == ' ' || character == '\n' || character == '\t'
		})
	case []any:
		for _, item := range typed {
			if text, ok := item.(string); ok {
				candidates = append(candidates, text)
			}
		}
	}
	seen := map[string]bool{}
	result := []string{}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || len(candidate) > 128 || !safeMetadataToken(candidate) || seen[candidate] {
			continue
		}
		seen[candidate] = true
		result = append(result, candidate)
		if len(result) == 100 {
			break
		}
	}
	sort.Strings(result)
	return result
}

func safeMetadataToken(value string) bool {
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || strings.ContainsRune("._:/@*()-", character) {
			continue
		}
		return false
	}
	return true
}

func safeDescriptorFields(metadata map[string]any) []string {
	result := []string{}
	for key := range metadata {
		key = strings.TrimSpace(key)
		if key == "" || len(key) > 64 || !safeMetadataToken(key) {
			continue
		}
		result = append(result, key)
	}
	sort.Strings(result)
	if len(result) > 100 {
		result = result[:100]
	}
	return result
}

func nonEmptyString(value any) bool {
	text, _ := value.(string)
	return strings.TrimSpace(text) != ""
}

func declaredListOrString(value any) bool {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed) != ""
	case []any:
		return len(typed) > 0
	}
	return false
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

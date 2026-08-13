package skillconfig

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseValidMetadataWithoutReturningInstructionBody(t *testing.T) {
	metadata := Parse([]byte("---\nname: code-review\ndescription: Review sensitive internal code\nlicense: Apache-2.0\ncompatibility: Requires internal systems\nallowed-tools: [Read, Grep]\n---\nprivate instructions\n"), "code-review")
	if !metadata.Valid || metadata.Name != "code-review" {
		t.Fatalf("valid skill metadata was not recognized: %#v", metadata)
	}
	if !metadata.DescriptionPresent || !metadata.LicenseDeclared || !metadata.CompatibilityDeclared || !metadata.AllowedToolsDeclared {
		t.Fatalf("safe descriptor-presence facts were not retained: %#v", metadata)
	}
	if metadata.DeclaredPurpose != "Review sensitive internal code" || metadata.License != "Apache-2.0" || metadata.Compatibility != "Requires internal systems" || strings.Join(metadata.AllowedTools, ",") != "Grep,Read" {
		t.Fatalf("bounded declarative metadata was not retained exactly: %#v", metadata)
	}
	if strings.Contains(fmt.Sprintf("%#v", metadata), "private instructions") {
		t.Fatalf("instruction body escaped the parser: %#v", metadata)
	}
	if strings.Join(metadata.DescriptorFields, ",") != "allowed-tools,compatibility,description,license,name" {
		t.Fatalf("descriptor field inventory is incomplete: %#v", metadata.DescriptorFields)
	}
}

func TestParseRejectsMissingOrMismatchedMetadata(t *testing.T) {
	for name, data := range map[string]string{
		"missing frontmatter": "# Instructions",
		"missing description": "---\nname: code-review\n---\n",
		"directory mismatch":  "---\nname: other\ndescription: Example\n---\n",
		"invalid name":        "---\nname: Code Review\ndescription: Example\n---\n",
	} {
		t.Run(name, func(t *testing.T) {
			if metadata := Parse([]byte(data), "code-review"); metadata.Valid {
				t.Fatalf("invalid skill was accepted: %#v", metadata)
			}
		})
	}
}

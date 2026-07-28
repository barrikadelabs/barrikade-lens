package skillconfig

import "testing"

func TestParseValidMetadataWithoutReturningDescription(t *testing.T) {
	metadata := Parse([]byte("---\nname: code-review\ndescription: Review sensitive internal code\n---\nprivate instructions\n"), "code-review")
	if !metadata.Valid || metadata.Name != "code-review" {
		t.Fatalf("valid skill metadata was not recognized: %#v", metadata)
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

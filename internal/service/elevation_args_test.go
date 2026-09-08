package service

import "testing"

func TestWindowsElevationArgumentQuoting(t *testing.T) {
	tests := map[string]string{
		"plain":                  "plain",
		"":                       `""`,
		"with space":             `"with space"`,
		`C:\Program Files\Lens\`: `"C:\Program Files\Lens\\"`,
		`say"hello`:              `"say\"hello"`,
	}
	for input, expected := range tests {
		if actual := quoteWindowsArg(input); actual != expected {
			t.Errorf("quoteWindowsArg(%q)=%q want %q", input, actual, expected)
		}
	}
	if actual := quotePowerShellLiteral("it's safe"); actual != `'it''s safe'` {
		t.Fatalf("PowerShell literal=%q", actual)
	}
}

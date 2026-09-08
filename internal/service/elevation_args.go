package service

import (
	"encoding/base64"
	"strings"
	"unicode/utf16"
)

func quoteWindowsArgs(args []string) []string {
	quoted := make([]string, len(args))
	for index, argument := range args {
		quoted[index] = quoteWindowsArg(argument)
	}
	return quoted
}

// quoteWindowsArg follows CommandLineToArgvW escaping rules.
func quoteWindowsArg(argument string) string {
	if argument != "" && !strings.ContainsAny(argument, " \t\n\v\"") {
		return argument
	}
	var result strings.Builder
	result.WriteByte('"')
	backslashes := 0
	for _, character := range argument {
		if character == '\\' {
			backslashes++
			continue
		}
		if character == '"' {
			result.WriteString(strings.Repeat("\\", backslashes*2+1))
			result.WriteRune(character)
			backslashes = 0
			continue
		}
		result.WriteString(strings.Repeat("\\", backslashes))
		backslashes = 0
		result.WriteRune(character)
	}
	result.WriteString(strings.Repeat("\\", backslashes*2))
	result.WriteByte('"')
	return result.String()
}

func quotePowerShellLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func encodePowerShell(script string) string {
	encoded := utf16.Encode([]rune(script))
	bytes := make([]byte, len(encoded)*2)
	for index, value := range encoded {
		bytes[index*2] = byte(value)
		bytes[index*2+1] = byte(value >> 8)
	}
	return base64.StdEncoding.EncodeToString(bytes)
}

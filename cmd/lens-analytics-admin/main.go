// Command lens-analytics-admin derives the pseudonyms needed for an
// authorized PostHog access or erasure request. Raw identifiers are read from
// stdin so they do not appear in shell history or process arguments.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/barrikadelabs/barrikade-lens/internal/hub"
)

func derivePseudonym(kind, raw string, salt []byte) (string, error) {
	if len(salt) < 32 {
		return "", fmt.Errorf("LENS_POSTHOG_ID_SALT must contain at least 32 bytes")
	}
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("a raw Lens identifier is required on stdin")
	}
	config := hub.ProductAnalyticsConfig{IDSalt: salt}
	switch kind {
	case "user":
		return config.UserID(raw), nil
	case "workspace":
		return config.WorkspaceID(raw), nil
	default:
		return "", fmt.Errorf("kind must be user or workspace")
	}
}

func main() {
	kind := flag.String("kind", "", "identifier kind: user or workspace")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "raw identifiers must be provided on stdin, not as arguments")
		os.Exit(2)
	}
	raw, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && len(raw) == 0 {
		fmt.Fprintln(os.Stderr, "could not read the raw Lens identifier from stdin")
		os.Exit(2)
	}
	pseudonym, err := derivePseudonym(*kind, strings.TrimSuffix(raw, "\n"), []byte(os.Getenv("LENS_POSTHOG_ID_SALT")))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	fmt.Println(pseudonym)
}

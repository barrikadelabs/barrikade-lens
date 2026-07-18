package hub

import (
	"errors"
	"testing"
)

func TestPermanentNormalizationErrors(t *testing.T) {
	for _, message := range []string{"out-of-order sequence 2", "load source: no rows in result set", "violates foreign key constraint"} {
		if !permanentNormalizationError(errors.New(message)) {
			t.Fatalf("expected permanent classification for %q", message)
		}
	}
	if permanentNormalizationError(errors.New("connection reset by peer")) {
		t.Fatal("transient database errors must remain retryable")
	}
}

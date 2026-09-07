package hub

import (
	"errors"
	"fmt"
	"testing"
)

func TestPermanentNormalizationErrors(t *testing.T) {
	for _, message := range []string{"load source: no rows in result set", "violates foreign key constraint"} {
		if !permanentNormalizationError(errors.New(message)) {
			t.Fatalf("expected permanent classification for %q", message)
		}
	}
	if permanentNormalizationError(errors.New("connection reset by peer")) {
		t.Fatal("transient database errors must remain retryable")
	}
	if permanentNormalizationError(fmt.Errorf("%w: sequence 3; source is at 3", errSnapshotAlreadyApplied)) {
		t.Fatal("an already-applied snapshot is an idempotent success")
	}
}

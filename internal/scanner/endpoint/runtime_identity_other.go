//go:build !darwin

package endpoint

import (
	"context"

	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
)

func collectRuntimeIdentityObservations(context.Context, Options) []discovery.RuntimeIdentityObservation {
	return nil
}

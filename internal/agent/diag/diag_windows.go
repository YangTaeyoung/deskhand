//go:build windows

package diag

import (
	"context"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// Supported reports whether Local reads this system: true on Windows.
const Supported = true

// Local returns the Source of this PC.
func Local() Source { return local{} }

type local struct{}

func (local) System(ctx context.Context) (System, error) { return readSystem(ctx) }

func (local) Services(_ context.Context, filter string) ([]Service, error) {
	return readServices(filter)
}

func (local) Events(ctx context.Context, q wire.DiagEventsQuery) (Events, error) {
	return readEvents(ctx, q)
}

func (local) Crashes(_ context.Context, since time.Time) ([]Dump, error) {
	return listCrashes(crashSources(), since), nil
}

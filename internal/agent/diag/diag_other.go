//go:build !windows

package diag

import (
	"context"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// Supported reports whether Local reads this system: false outside Windows.
const Supported = false

// Local returns the Source of this PC, whose readers return ErrUnsupported
// outside Windows.
func Local() Source { return unsupported{} }

type unsupported struct{}

func (unsupported) System(context.Context) (System, error) { return System{}, ErrUnsupported }

func (unsupported) Services(context.Context, string) ([]Service, error) { return nil, ErrUnsupported }

func (unsupported) Events(context.Context, wire.DiagEventsQuery) (Events, error) {
	return Events{}, ErrUnsupported
}

func (unsupported) Crashes(context.Context, time.Time) ([]Dump, error) { return nil, ErrUnsupported }

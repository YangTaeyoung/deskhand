package screen

import (
	"context"
	"runtime"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// processCPU returns the CPU time the test process has used, kernel and
// user.
func processCPU(t *testing.T) time.Duration {
	t.Helper()
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(windows.CurrentProcess(), &created, &exited, &kernel, &user); err != nil {
		t.Fatalf("GetProcessTimes: %v", err)
	}
	ticks := func(f windows.Filetime) int64 { return int64(f.HighDateTime)<<32 | int64(f.LowDateTime) }
	return time.Duration(ticks(kernel)+ticks(user)) * 100
}

func TestPipelineCPU(t *testing.T) {
	d := streamDisplay(t)
	const measure = 30 * time.Second
	for _, motion := range []bool{false, true} {
		name := "static"
		if motion {
			name = "motion"
		}
		t.Run(name, func(t *testing.T) {
			if motion {
				w, h := min(1280, d.Width), min(720, d.Height)
				startMotion(t, d.X+(d.Width-w)/2, d.Y+(d.Height-h)/2, w, h, 40)
				time.Sleep(time.Second)
			}
			rec := &recorder{}
			ctx, cancel := context.WithTimeout(context.Background(), measure+5*time.Second)
			defer cancel()
			errc := make(chan error, 1)
			go func() { errc <- New(Options{}).Start(ctx, wire.StreamHeader{S: "video", Display: d.Index}, rec.out) }()
			time.Sleep(2 * time.Second)
			cpu0, wall0, aus0 := processCPU(t), time.Now(), len(rec.aus(t))
			time.Sleep(measure)
			cpu1, wall1, aus1 := processCPU(t), time.Now(), len(rec.aus(t))
			cancel()
			if err := <-errc; err != nil {
				t.Fatalf("Start: %v", err)
			}
			wall := wall1.Sub(wall0)
			oneCPU := float64(cpu1-cpu0) / float64(wall) * 100
			t.Logf("%s: display %d %dx%d, %d access units in %v (%.1f fps); process CPU %v = %.1f%% of one logical CPU, %.2f%% of all %d",
				name, d.Index, d.Width, d.Height, aus1-aus0, wall.Round(time.Millisecond), float64(aus1-aus0)/wall.Seconds(),
				(cpu1 - cpu0).Round(time.Millisecond), oneCPU, oneCPU/float64(runtime.NumCPU()), runtime.NumCPU())
			if motion && float64(aus1-aus0)/wall.Seconds() < 30 {
				t.Errorf("%.1f fps with a moving window on the display", float64(aus1-aus0)/wall.Seconds())
			}
		})
	}
}

package main

import (
	"bufio"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

// gpuSample is one reading of nvidia-smi: the probe clock when its line
// was read, the performance state and the graphics and video clocks.
type gpuSample struct {
	US          int64  `json:"us"`
	PState      string `json:"pstate"`
	GraphicsMHz int    `json:"graphics_mhz"`
	VideoMHz    int    `json:"video_mhz"`
}

// gpuSampler runs nvidia-smi as a child that prints a reading every
// interval and keeps the readings.
type gpuSampler struct {
	cmd  *exec.Cmd
	done chan struct{}

	mu      sync.Mutex
	samples []gpuSample
}

// startGPU starts path (nvidia-smi) with a read-only query of the
// performance state and clocks every intervalMS milliseconds.
func startGPU(path string, intervalMS int) (*gpuSampler, error) {
	cmd := exec.Command(path, "--query-gpu=pstate,clocks.gr,clocks.video", "--format=csv,noheader,nounits", "-lms", strconv.Itoa(intervalMS))
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	g := &gpuSampler{cmd: cmd, done: make(chan struct{})}
	go func() {
		defer close(g.done)
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			at := nowUS()
			if s, ok := parseGPULine(sc.Text()); ok {
				s.US = at
				g.mu.Lock()
				g.samples = append(g.samples, s)
				g.mu.Unlock()
			}
		}
	}()
	return g, nil
}

// stop ends the child and returns the readings.
func (g *gpuSampler) stop() []gpuSample {
	_ = g.cmd.Process.Kill()
	<-g.done
	_ = g.cmd.Wait()
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.samples
}

// parseGPULine reads a line "P8, 300, 540" of the query of startGPU.
func parseGPULine(line string) (gpuSample, bool) {
	parts := strings.Split(line, ",")
	if len(parts) != 3 {
		return gpuSample{}, false
	}
	p := strings.TrimSpace(parts[0])
	gr, err1 := strconv.Atoi(strings.TrimSpace(parts[1]))
	video, err2 := strconv.Atoi(strings.TrimSpace(parts[2]))
	if !strings.HasPrefix(p, "P") || err1 != nil || err2 != nil {
		return gpuSample{}, false
	}
	return gpuSample{PState: p, GraphicsMHz: gr, VideoMHz: video}, true
}

// gpuAt returns the last reading at or before us that is at most maxAgeUS
// old.
func gpuAt(samples []gpuSample, us, maxAgeUS int64) (gpuSample, bool) {
	var last gpuSample
	found := false
	for _, s := range samples {
		if s.US > us {
			break
		}
		last, found = s, true
	}
	if !found || us-last.US > maxAgeUS {
		return gpuSample{}, false
	}
	return last, true
}

// gpuState names a reading as "P5 945 MHz".
func gpuState(s gpuSample) string { return fmt.Sprintf("%s %d MHz", s.PState, s.VideoMHz) }

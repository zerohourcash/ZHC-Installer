package main

import (
	"encoding/json"
	"io"
	"os"
	"sync"
	"time"
)

type progressEvent struct {
	Type    string  `json:"type"`
	Phase   string  `json:"phase,omitempty"`
	Done    int64   `json:"done,omitempty"`
	Total   int64   `json:"total,omitempty"`
	Speed   float64 `json:"speed,omitempty"`
	Message string  `json:"message,omitempty"`
}

var eventOutput io.Writer
var eventMutex sync.Mutex

func enableProgressEvents(enabled bool) {
	if enabled {
		eventOutput = os.Stderr
	}
}

func emitProgress(event progressEvent) {
	eventMutex.Lock()
	defer eventMutex.Unlock()
	if eventOutput != nil {
		_ = json.NewEncoder(eventOutput).Encode(event)
	}
}

func (r *installerRunTelemetry) setPhase(phase string) {
	r.Phase = phase
	emitProgress(progressEvent{Type: "phase", Phase: phase})
}

type byteProgress struct {
	phase       string
	done, total int64
	last        time.Time
}

func (p *byteProgress) Write(data []byte) (int, error) {
	p.done += int64(len(data))
	if time.Since(p.last) >= 200*time.Millisecond || p.done == p.total {
		emitProgress(progressEvent{Type: "progress", Phase: p.phase, Done: p.done, Total: p.total})
		p.last = time.Now()
	}
	return len(data), nil
}

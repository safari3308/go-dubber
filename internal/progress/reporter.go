package progress

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Reporter decouples pipeline progress reporting from CLI spinners or SSE streams.
type Reporter interface {
	Update(stage string, percent float64, message string)
	Info(message string)
	Warn(message string)
	Error(message string)
}

// NopReporter discards all progress updates.
type NopReporter struct{}

func (NopReporter) Update(stage string, percent float64, message string) {}
func (NopReporter) Info(message string)                                  {}
func (NopReporter) Warn(message string)                                  {}
func (NopReporter) Error(message string)                                 {}

// FuncReporter adapts a callback function into a Reporter.
type FuncReporter struct {
	OnUpdate func(stage string, percent float64, message string)
	OnLog    func(level, message string)
}

func (f FuncReporter) Update(stage string, percent float64, message string) {
	if f.OnUpdate != nil {
		f.OnUpdate(stage, percent, message)
	}
}

func (f FuncReporter) Info(message string) {
	if f.OnLog != nil {
		f.OnLog("info", message)
	}
}

func (f FuncReporter) Warn(message string) {
	if f.OnLog != nil {
		f.OnLog("warn", message)
	}
}

func (f FuncReporter) Error(message string) {
	if f.OnLog != nil {
		f.OnLog("error", message)
	}
}

// Spinner provides terminal spinner animations for CLI mode.
type Spinner struct {
	stopChan chan struct{}
	doneChan chan struct{}
	mu       sync.Mutex
	msg      string
}

// StartSpinner starts progress spinner animation.
func StartSpinner(msg string) *Spinner {
	s := &Spinner{
		stopChan: make(chan struct{}),
		doneChan: make(chan struct{}),
		msg:      msg,
	}

	go func() {
		chars := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		i := 0
		start := time.Now()

		for {
			select {
			case <-s.stopChan:
				fmt.Printf("\r\033[K")
				close(s.doneChan)
				return
			default:
				s.mu.Lock()
				currentMsg := s.msg
				s.mu.Unlock()

				elapsed := time.Since(start)
				mins := int(elapsed.Minutes())
				secs := int(elapsed.Seconds()) % 60

				fmt.Printf("\r\033[K    %s %s [%02d:%02d]", chars[i], currentMsg, mins, secs)

				i = (i + 1) % len(chars)
				time.Sleep(100 * time.Millisecond)
			}
		}
	}()

	return s
}

// UpdateMessage dynamically updates the spinner text while spinning.
func (s *Spinner) UpdateMessage(msg string) {
	s.mu.Lock()
	s.msg = msg
	s.mu.Unlock()
}

// Update implements Reporter so a Spinner can be passed directly into pipeline stages.
func (s *Spinner) Update(stage string, percent float64, message string) {
	s.UpdateMessage(message)
}

func (s *Spinner) Info(message string)  { s.UpdateMessage(message) }
func (s *Spinner) Warn(message string)  { s.UpdateMessage(message) }
func (s *Spinner) Error(message string) { s.UpdateMessage(message) }

// Stop stops progress spinner animation and prints final result.
func (s *Spinner) Stop(finalMsg string, isError bool) {
	close(s.stopChan)
	<-s.doneChan

	if finalMsg != "" {
		trimmed := strings.TrimSpace(finalMsg)
		if isError {
			if strings.HasPrefix(trimmed, "❌") {
				fmt.Printf("\r\033[K    %s\n", trimmed)
			} else {
				fmt.Printf("\r\033[K    ❌ %s\n", trimmed)
			}
		} else {
			if strings.HasPrefix(trimmed, "✅") {
				fmt.Printf("\r\033[K    %s\n", trimmed)
			} else {
				fmt.Printf("\r\033[K    ✅ %s\n", trimmed)
			}
		}
	} else {
		fmt.Printf("\r\033[K")
	}
}

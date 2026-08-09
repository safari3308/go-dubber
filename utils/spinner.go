package utils

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

type Spinner struct {
	stopChan chan struct{}
	doneChan chan struct{}
	mu       sync.Mutex
	msg      string
}

// StartSpinner starts progress spinner animation
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

// UpdateMessage dynamically updates the spinner text while spinning
func (s *Spinner) UpdateMessage(msg string) {
	s.mu.Lock()
	s.msg = msg
	s.mu.Unlock()
}

// Stop stops progress spinner animation and prints final result
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



package utils

import (
	"fmt"
	"strings"
	"time"
)

type Spinner struct {
	stopChan chan struct{}
	doneChan chan struct{}
}

// StartSpinner starts progress spinner animation
func StartSpinner(msg string) *Spinner {
	s := &Spinner{
		stopChan: make(chan struct{}),
		doneChan: make(chan struct{}),
	}

	go func() {
		chars := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		i := 0
		start := time.Now()

		for {
			select {
			case <-s.stopChan:
				fmt.Printf("\r%s\r", strings.Repeat(" ", 100))
				close(s.doneChan)
				return
			default:
				elapsed := time.Since(start)
				mins := int(elapsed.Minutes())
				secs := int(elapsed.Seconds()) % 60
				
				fmt.Printf("\r    %s %s [%02d:%02d] ", chars[i], msg, mins, secs)
				
				i = (i + 1) % len(chars)
				time.Sleep(100 * time.Millisecond)
			}
		}
	}()

	return s
}

// Stop stops progress spinner animation and prints final result
func (s *Spinner) Stop(finalMsg string, isError bool) {
	close(s.stopChan)
	<-s.doneChan
	
	if finalMsg != "" {
		if isError {
			fmt.Printf("    ❌ %s\n", finalMsg)
		} else {
			fmt.Printf("    ✅ %s\n", finalMsg)
		}
	}
}


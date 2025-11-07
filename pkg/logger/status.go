// MIT License
//
// Copyright (c) 2025 lok8s
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package logger

import (
	"fmt"
	"io"

	"github.com/sirupsen/logrus"
)

// Status is used to track ongoing status in a CLI, with a nice loading spinner
// when attached to a terminal
type Status struct {
	spinner        *Spinner
	status         string
	logger         *logrus.Logger
	originalWriter io.Writer // original logger output, restored on End
	// for controlling coloring etc
	successFormat string
	failureFormat string
}

// StatusForLogger returns a new status object for the logger.
// If the logger's output is a terminal and supports spinners, that spinner
// will be used for the status.
// Similar to kind's StatusForLogger implementation.
func StatusForLogger(l *logrus.Logger) *Status {
	s := &Status{
		logger:        l,
		successFormat: "✓ %s\n",
		failureFormat: "✗ %s\n",
	}

	// Check if we're writing to a smart terminal (supports colors/spinners)
	if writer := l.Out; writer != nil {
		// Check if the writer is already a Spinner (like kind does)
		if spinner, ok := writer.(*Spinner); ok {
			s.spinner = spinner
			// use colored success / failure messages
			s.successFormat = "\x1b[32m✓\x1b[0m %s\n"
			s.failureFormat = "\x1b[31m✗\x1b[0m %s\n"
		} else if IsSmartTerminal(writer) {
			// Writer is a smart terminal, create a spinner for it
			spinner := NewSpinner(writer)
			s.spinner = spinner
			// use colored success / failure messages
			s.successFormat = "\x1b[32m✓\x1b[0m %s\n"
			s.failureFormat = "\x1b[31m✗\x1b[0m %s\n"
		}
	}

	return s
}

// NewStatus creates a new status object using the default logger.
// This provides animated spinner feedback for long-running operations.
// Example usage:
//
//	status := logger.NewStatus()
//	status.Start("creating cluster")
//	defer func() {
//	    if err != nil {
//	        status.End(false)
//	    } else {
//	        status.End(true)
//	    }
//	}()
//	// ... do work ...
func NewStatus() *Status {
	return StatusForLogger(log)
}

// Start starts a new phase of the status, if attached to a terminal
// there will be a loading spinner with this status
func (s *Status) Start(status string) {
	s.End(true)
	// set new status
	s.status = status
	if s.spinner != nil {
		// Save the original writer and wrap logger output with spinner
		// This ensures all log writes go through the spinner's Write() method
		// which will properly interrupt the spinner animation
		s.originalWriter = s.logger.Out
		s.logger.SetOutput(s.spinner)
		// Update formatter colors since we now have a spinner (smart terminal)
		updateFormatterColors()
		s.spinner.SetSuffix(fmt.Sprintf(" %s ", s.status))
		s.spinner.Start()
	} else {
		s.logger.Infof(" • %s  ...", s.status)
	}
}

// End completes the current status, ending any previous spinning and
// marking the status as success or failure
func (s *Status) End(success bool) {
	if s.status == "" {
		return
	}

	if s.spinner != nil {
		// Stop the spinner first
		s.spinner.Stop()
		// Restore the original logger output writer
		if s.originalWriter != nil {
			s.logger.SetOutput(s.originalWriter)
			// Update formatter colors since we restored the original writer
			updateFormatterColors()
			s.originalWriter = nil
		}
		// Clear the spinner line (go to beginning and clear to end)
		fmt.Fprint(s.logger.Out, "\r\x1b[K")
	}
	if success {
		s.logger.Infof(s.successFormat, s.status)
	} else {
		s.logger.Infof(s.failureFormat, s.status)
	}

	s.status = ""
}

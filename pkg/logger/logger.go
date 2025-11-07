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
	"os"

	"github.com/sirupsen/logrus"
)

var log = logrus.New()

func init() {
	// Set default configuration
	log.SetOutput(os.Stdout)

	// Use custom formatter that colors ✓ and ✗ characters
	baseFormatter := &logrus.TextFormatter{
		FullTimestamp: true,
		ForceColors:   true,
	}

	formatter := &ColoredFormatter{
		TextFormatter: baseFormatter,
		colorEnabled:  ColorEnabled(),
	}

	log.SetFormatter(formatter)
	log.SetLevel(logrus.InfoLevel)
}

// updateFormatterColors updates the formatter's colorEnabled state
// This is useful when the logger output changes (e.g., when a spinner is added)
func updateFormatterColors() {
	if formatter, ok := log.Formatter.(*ColoredFormatter); ok {
		formatter.colorEnabled = ColorEnabled()
	}
}

// ColorEnabled returns true if the logger is writing to a terminal that supports colors.
// This can be used by callers to determine if they should output colored text.
func ColorEnabled() bool {
	writer := log.Out
	if writer == nil {
		return false
	}
	// Check if writer is a Spinner (which indicates smart terminal)
	if _, ok := writer.(*Spinner); ok {
		return true
	}
	// Check if writer is a smart terminal
	return IsSmartTerminal(writer)
}

// SetLevel sets the logging level
func SetLevel(level logrus.Level) {
	log.SetLevel(level)
}

// GetLogger returns the configured logger instance
func GetLogger() *logrus.Logger {
	return log
}

// Info logs an info message
func Info(args ...interface{}) {
	log.Info(args...)
}

// Infof logs a formatted info message
func Infof(format string, args ...interface{}) {
	log.Infof(format, args...)
}

// Debug logs a debug message
func Debug(args ...interface{}) {
	log.Debug(args...)
}

// Debugf logs a formatted debug message
func Debugf(format string, args ...interface{}) {
	log.Debugf(format, args...)
}

// Warn logs a warning message
func Warn(args ...interface{}) {
	log.Warn(args...)
}

// Warnf logs a formatted warning message
func Warnf(format string, args ...interface{}) {
	log.Warnf(format, args...)
}

// Error logs an error message
func Error(args ...interface{}) {
	log.Error(args...)
}

// Errorf logs a formatted error message
func Errorf(format string, args ...interface{}) {
	log.Errorf(format, args...)
}

// Fatal logs a fatal message and exits
func Fatal(args ...interface{}) {
	log.Fatal(args...)
}

// Fatalf logs a formatted fatal message and exits
func Fatalf(format string, args ...interface{}) {
	log.Fatalf(format, args...)
}

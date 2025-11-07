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
	"strings"

	"github.com/sirupsen/logrus"
)

// ColoredFormatter wraps logrus.TextFormatter and adds color to ✓ and ✗ characters
type ColoredFormatter struct {
	*logrus.TextFormatter
	colorEnabled bool
}

// Format formats the log entry and adds color to ✓ and ✗ characters
func (f *ColoredFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	// call the base formatter first
	data, err := f.TextFormatter.Format(entry)
	if err != nil {
		return nil, err
	}

	// if colors are not enabled, return as-is
	if !f.colorEnabled {
		return data, nil
	}

	// color ✓ in green and ✗ in red, with a space before them
	result := string(data)

	// define colored sequences to avoid double-coloring
	greenCheck := "\x1b[32m ✓\x1b[0m"
	redCross := "\x1b[31m ✗\x1b[0m"

	// replace ✓ with green ✓ (with space), but avoid double-coloring
	if strings.Contains(result, "✓") {
		// first, replace all already-colored ✓ to a placeholder to avoid re-coloring
		// use a placeholder that's very unlikely to appear in log messages
		placeholder := "___LOK8S_GREEN_CHECK_PLACEHOLDER___"
		temp := strings.ReplaceAll(result, greenCheck, placeholder)
		// protect escape sequences that might contain ✓
		// replace any remaining colored ✓ that might exist
		temp = strings.ReplaceAll(temp, "\x1b[32m✓\x1b[0m", placeholder)
		// now replace uncolored ✓ with space before it
		// handle cases where space already exists: " ✓" -> " \x1b[32m✓\x1b[0m"
		temp = strings.ReplaceAll(temp, " ✓", " \x1b[32m✓\x1b[0m")
		// handle cases where no space exists: "✓" -> "\x1b[32m ✓\x1b[0m"
		temp = strings.ReplaceAll(temp, "✓", "\x1b[32m ✓\x1b[0m")
		// restore the already-colored ones
		result = strings.ReplaceAll(temp, placeholder, greenCheck)
	}

	// replace ✗ with red ✗ (with space), but avoid double-coloring
	if strings.Contains(result, "✗") {
		// first, replace all already-colored ✗ to a placeholder to avoid re-coloring
		placeholder := "___LOK8S_RED_CROSS_PLACEHOLDER___"
		temp := strings.ReplaceAll(result, redCross, placeholder)
		// protect escape sequences that might contain ✗
		// replace any remaining colored ✗ that might exist
		temp = strings.ReplaceAll(temp, "\x1b[31m✗\x1b[0m", placeholder)
		// now replace uncolored ✗ with space before it
		// handle cases where space already exists: " ✗" -> " \x1b[31m✗\x1b[0m"
		temp = strings.ReplaceAll(temp, " ✗", " \x1b[31m✗\x1b[0m")
		// handle cases where no space exists: "✗" -> "\x1b[31m ✗\x1b[0m"
		temp = strings.ReplaceAll(temp, "✗", "\x1b[31m ✗\x1b[0m")
		// restore the already-colored ones
		result = strings.ReplaceAll(temp, placeholder, redCross)
	}

	return []byte(result), nil
}

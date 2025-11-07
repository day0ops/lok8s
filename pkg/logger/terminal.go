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
	"io"
	"os"
	"runtime"

	"golang.org/x/term"
)

// IsSmartTerminal returns true if w is a terminal and supports colors/spinners.
// Based on kind's terminal detection logic, but using golang.org/x/term for proper TTY detection.
func IsSmartTerminal(w io.Writer) bool {
	file, ok := w.(*os.File)
	if !ok {
		return false
	}

	// Use golang.org/x/term to check if it's actually a terminal (TTY)
	if !term.IsTerminal(int(file.Fd())) {
		return false
	}

	// Check TERM environment variable (Unix-like systems)
	// Skip this check on Windows as it's not relevant
	if runtime.GOOS != "windows" {
		termEnv := os.Getenv("TERM")
		// If TERM is "dumb", it's not a smart terminal
		if termEnv == "dumb" {
			return false
		}
		// If TERM is empty, it might not be a real terminal
		// But if term.IsTerminal returned true, we trust it
	}

	return true
}

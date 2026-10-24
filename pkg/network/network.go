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

package network

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/day0ops/lok8s/pkg/logger"
)

// Network represents a network configuration
type Network struct {
	// The name of the network
	Name string

	// The name of the bridge to create
	Bridge string

	// Subnet of the network
	Subnet string

	// QEMU Connection URI
	ConnectionURI string
}

// ValidateSudoAccess validates sudo access by checking if a sudo session is
// already active, prompting for a password otherwise. Used by any backend
// that needs a one-time privileged setup step: minikube's vmnet-helper
// (network_darwin.go) and the native provider's Lima backend
// (pkg/cluster/native/lima_client.go), on whichever platform each runs on.
func ValidateSudoAccess(ctx context.Context) error {
	// first, try to validate sudo without prompting (non-interactive)
	// this will succeed if sudo session is already active
	cmd := exec.CommandContext(ctx, "sudo", "-n", "-v")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err == nil {
		return nil
	}

	// sudo session is not active, prompt for password
	logger.Debugf("sudo session not active, prompting for password")
	cmd = exec.CommandContext(ctx, "sudo", "-v")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("sudo validation failed: %w", err)
	}
	return nil
}

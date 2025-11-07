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

//go:build darwin

package network

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/day0ops/lok8s/pkg/logger"
)

const (
	vmnetInstallPath = "/opt/vmnet-helper"
	vmnetHelperPath  = vmnetInstallPath + "/bin/vmnet-helper"
)

// PrerequisiteChecks check if all the required pre-reqs are present
func (n *Network) PrerequisiteChecks() bool {
	// check if vmnet-helper exists
	present, err := isVmnetHelperPresent()
	if err != nil {
		logger.Debugf("vmnet-helper check failed: %v", err)
		return false
	}
	if !present {
		logger.Debugf("vmnet-helper is not installed")
		return false
	}

	return true
}

// EnsureNetwork creates a vmnet interface using vmnet-helper
func (n *Network) EnsureNetwork() error {
	logger.Debugf("ensuring vmnet network: %s with subnet %s", n.Name, n.Subnet)

	// create a context for all sudo commands to maintain session
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// validate sudo access early, before starting spinner
	// this prevents sudo prompt from interleaving with spinner animation
	if err := validateSudoAccess(ctx); err != nil {
		return fmt.Errorf("sudo access required for network setup: %w", err)
	}

	status := logger.NewStatus()
	status.Start(fmt.Sprintf("ensuring network %s", n.Name))

	// ensure vmnet-helper is installed
	if err := ensureInstalled(ctx); err != nil {
		status.End(false)
		logger.Warnf("vmnet-helper installation failed: %v", err)
		return err
	}

	if err := configureFirewall(ctx); err != nil {
		status.End(false)
		logger.Warnf("unable to configure macOs firewall: %v", err)
		return err
	}

	status.End(true)
	return nil
}

// DeleteNetwork deletes a vmnet interface using vmnet-helper
func (n *Network) DeleteNetwork(force bool) error {
	logger.Debugf("deleting vmnet network: %s (force: %v)", n.Name, force)

	// create a context for all sudo commands to maintain session
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// validate sudo access early if force deletion is requested (requires sudo)
	if force {
		if err := validateSudoAccess(ctx); err != nil {
			return fmt.Errorf("sudo access required for force deletion: %w", err)
		}
	}

	status := logger.NewStatus()
	status.Start(fmt.Sprintf("deleting network %s", n.Name))
	defer status.End(true)

	// check if vmnet-helper is available
	present, err := isVmnetHelperPresent()
	if err != nil {
		logger.Debugf("vmnet-helper check failed: %v", err)
	}
	if !present {
		logger.Debugf("vmnet-helper is not installed, skipping process check")
	}

	// ensure vmnet-helper processes are terminated before deletion
	if isPresent, err := isVmnetHelperProcessRunning(); err != nil || !isPresent {
		logger.Warnf("failed to terminate vmnet-helper processes")
	}

	// if force flag is set, delete the vmnet-helper installation path
	if force {
		if err := deleteVmnetInstallPath(ctx); err != nil {
			logger.Warnf("failed to delete vmnet-helper installation path: %v", err)
			// continue with network deletion even if path deletion fails
		} else {
			logger.Infof("✓ deleted vmnet-helper installation path: %s", vmnetInstallPath)
		}
	}

	return nil
}

// ensureInstalled ensures vmnet-helper is installed
func ensureInstalled(ctx context.Context) error {
	logger.Debugf("ensuring vmnet-helper is installed")

	// check if vmnet-helper is available
	present, err := isVmnetHelperPresent()
	if err != nil {
		logger.Debugf("vmnet-helper check failed: %v", err)
	}
	if !present {
		logger.Debugf("vmnet-helper is not installed, attempting to install...")

		if err := installVmnetHelper(ctx); err != nil {
			return fmt.Errorf("failed to install vmnet-helper: %w", err)
		}

		logger.Debugf("✓ vmnet-helper installed successfully")

		return nil
	}

	logger.Debugf("vmnet-helper is already installed")
	return nil
}

// installVmnetHelper downloads and installs vmnet-helper
func installVmnetHelper(ctx context.Context) error {
	logger.Debugf("installing vmnet-helper")

	// download the tar.gz archive
	archiveURL := "https://github.com/minikube-machine/vmnet-helper/releases/latest/download/vmnet-helper.tar.gz"
	logger.Debugf("downloading vmnet-helper archive from %s", archiveURL)

	resp, err := http.Get(archiveURL)
	if err != nil {
		return fmt.Errorf("failed to download vmnet-helper archive: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download vmnet-helper archive: HTTP %d", resp.StatusCode)
	}

	// create temporary file for the archive
	tmpFile, err := os.CreateTemp("", "vmnet-helper-*.tar.gz")
	if err != nil {
		return fmt.Errorf("failed to create temporary file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	// write the archive content
	_, err = io.Copy(tmpFile, resp.Body)
	if err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to write archive: %w", err)
	}
	tmpFile.Close()

	// extract the archive to /opt/vmnet-helper using sudo
	logger.Debugf("extracting vmnet-helper to %s...", vmnetInstallPath)
	cmd := exec.CommandContext(ctx, "sudo", "tar", "--extract", "--file", tmpFile.Name(), "--directory", "/", "opt/vmnet-helper")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to extract vmnet-helper archive: %w", err)
	}

	// configure sudoers if the sudoers file exists
	sudoersFile := vmnetInstallPath + "/share/doc/vmnet-helper/sudoers.d/vmnet-helper"
	if _, err := os.Stat(sudoersFile); err == nil {
		logger.Infof("configuring sudoers for vmnet-helper...")
		sudoCmd := exec.CommandContext(ctx, "sudo", "install", "-m", "0640", sudoersFile, "/etc/sudoers.d/")
		if err := sudoCmd.Run(); err != nil {
			logger.Warnf("failed to configure sudoers (this is optional): %v", err)
		} else {
			logger.Debugf("✓ sudoers configured for vmnet-helper")
		}
	}

	logger.Debugf("✓ vmnet-helper installation completed")
	return nil
}

// validateSudoAccess validates sudo access by checking if sudo session is active
func validateSudoAccess(ctx context.Context) error {
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

// isVmnetHelperPresent checks if vmnet-helper script is available
func isVmnetHelperPresent() (bool, error) {
	// check if the file exists first (for absolute paths)
	if _, err := os.Stat(vmnetHelperPath); os.IsNotExist(err) {
		logger.Debugf("vmnet-helper binary not found at %s", vmnetHelperPath)
		return false, nil
	} else if err != nil {
		// Other errors (permissions, etc.)
		return false, fmt.Errorf("failed to check vmnet-helper: %w", err)
	}

	// check if vmnet-helper script is executable via LookPath
	if _, err := exec.LookPath(vmnetHelperPath); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			logger.Debugf("vmnet-helper binary not found in PATH")
			return false, nil
		}
		return false, fmt.Errorf("failed to locate vmnet-helper: %w", err)
	}

	// verify it's actually executable by running --version
	cmd := exec.Command(vmnetHelperPath, "--version")
	if err := cmd.Run(); err != nil {
		return false, fmt.Errorf("vmnet-helper found but not executable: %w", err)
	}

	logger.Debugf("vmnet-helper is available")
	return true, nil
}

// configureFirewall configures darwin firewall for minikube networking
func configureFirewall(ctx context.Context) error {
	logger.Debug("configuring darwin firewall for minikube networking")

	// add bootpd to firewall
	cmd := exec.CommandContext(ctx, "sudo", "/usr/libexec/ApplicationFirewall/socketfilterfw", "--add", "/usr/libexec/bootpd")
	if err := cmd.Run(); err != nil {
		logger.Warnf("failed to add bootpd to firewall (may already be added): %v", err)
	} else {
		logger.Debug("successfully added bootpd to firewall")
	}

	// unblock bootpd in firewall
	cmd = exec.CommandContext(ctx, "sudo", "/usr/libexec/ApplicationFirewall/socketfilterfw", "--unblock", "/usr/libexec/bootpd")
	if err := cmd.Run(); err != nil {
		logger.Warnf("failed to unblock bootpd in firewall (may already be unblocked): %v", err)
	} else {
		logger.Debug("successfully unblocked bootpd in firewall")
	}

	return nil
}

// isVmnetHelperProcessRunning ensures vmnet-helper processes are terminated
func isVmnetHelperProcessRunning() (bool, error) {
	logger.Debugf("checking for running vmnet-helper processes")

	// find vmnet-helper processes using ps command
	cmd := exec.Command("ps", "-axo", "pid,comm", "-c")
	output, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("failed to list processes: %w", err)
	}

	lines := strings.Split(string(output), "\n")
	var vmnetPIDs []string

	// look for vmnet-helper processes
	for _, line := range lines {
		if strings.Contains(line, "vmnet-helper") {
			fields := strings.Fields(line)
			if len(fields) >= 1 {
				vmnetPIDs = append(vmnetPIDs, fields[0])
			}
		}
	}

	if len(vmnetPIDs) == 0 {
		logger.Debugf("no vmnet-helper processes found")
		return true, nil
	} else {
		logger.Warnf("found %d vmnet-helper processes", len(vmnetPIDs))
		logger.Warnf("vmnet-helper process list: %v", vmnetPIDs)
		return false, nil
	}
}

// deleteVmnetInstallPath deletes the vmnet-helper installation directory
func deleteVmnetInstallPath(ctx context.Context) error {
	logger.Debugf("deleting vmnet-helper installation path: %s", vmnetInstallPath)

	// Check if the path exists
	if _, err := os.Stat(vmnetInstallPath); os.IsNotExist(err) {
		logger.Debugf("vmnet-helper installation path does not exist: %s", vmnetInstallPath)
		return nil
	}

	// Use sudo to remove the directory and all its contents
	cmd := exec.CommandContext(ctx, "sudo", "rm", "-rf", vmnetInstallPath)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to delete vmnet-helper installation path %s: %w", vmnetInstallPath, err)
	}

	logger.Debugf("successfully deleted vmnet-helper installation path: %s", vmnetInstallPath)
	return nil
}

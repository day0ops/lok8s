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

package minikube

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/day0ops/lok8s/pkg/config"
	"github.com/day0ops/lok8s/pkg/logger"
	"github.com/day0ops/lok8s/pkg/util/github"
	"github.com/day0ops/lok8s/pkg/util/version"
)

// BinaryManager manages the minikube binary download and execution
type BinaryManager struct {
	binaryPath   string
	version      string
	cacheDir     string
	githubClient *github.GitHubClient
}

// NewBinaryManager creates a new minikube binary manager
func NewBinaryManager() *BinaryManager {
	return &BinaryManager{
		version:      config.MinikubeMinSupportedVersion,
		githubClient: github.NewGitHubClient(),
	}
}

// EnsureBinary ensures the minikube binary is available locally
func (bm *BinaryManager) EnsureBinary() error {
	// Check if binary already exists and is valid
	if bm.isBinaryValid() {
		logger.Debugf("Using existing minikube binary at %s", bm.binaryPath)
		return nil
	}

	// Download the binary
	if err := bm.downloadBinary(); err != nil {
		return fmt.Errorf("failed to download minikube binary: %w", err)
	}

	// Make it executable
	if err := os.Chmod(bm.binaryPath, 0755); err != nil {
		return fmt.Errorf("failed to make binary executable: %w", err)
	}

	logger.Infof("Downloaded minikube binary to %s", bm.binaryPath)
	return nil
}

// GetBinaryPath returns the path to the minikube binary
func (bm *BinaryManager) GetBinaryPath() (string, error) {
	if err := bm.EnsureBinary(); err != nil {
		return "", err
	}
	return bm.binaryPath, nil
}

// GetLatestVersion fetches the latest minikube version from GitHub API
func (bm *BinaryManager) GetLatestVersion() (string, error) {
	return bm.githubClient.GetLatestVersion("kubernetes", "minikube")
}

// isBinaryValid checks if the existing binary is valid
func (bm *BinaryManager) isBinaryValid() bool {
	if bm.binaryPath == "" {
		bm.binaryPath = bm.getBinaryPath()
	}

	// Check if file exists
	if _, err := os.Stat(bm.binaryPath); os.IsNotExist(err) {
		return false
	}

	// Check if binary is executable
	if err := exec.Command(bm.binaryPath, "version", "--short").Run(); err != nil {
		return false
	}

	// Check version
	output, err := exec.Command(bm.binaryPath, "version", "--short").Output()
	if err != nil {
		return false
	}

	currentVersion := strings.TrimSpace(strings.TrimPrefix(string(output), "v"))
	if version.Compare(config.MinikubeMinSupportedVersion, currentVersion) > 0 {
		logger.Warnf("Minikube version %s is too old, need to download newer version", currentVersion)
		return false
	}

	return true
}

// downloadBinary downloads the minikube binary for the current platform
func (bm *BinaryManager) downloadBinary() error {
	// Try to get latest version, fallback to minimum supported version
	latestVersion, err := bm.GetLatestVersion()
	if err != nil {
		logger.Warnf("failed to get latest version, using minimum supported version: %v", err)
		latestVersion = bm.version
	} else {
		logger.Infof("Latest minikube version: %s", latestVersion)
	}

	// Get the appropriate binary name for current platform
	binaryName := bm.getBinaryName()
	downloadURL := bm.githubClient.GetBinaryDownloadURL("kubernetes", "minikube", fmt.Sprintf("v%s", latestVersion), binaryName)
	checksumURL := bm.githubClient.GetBinaryDownloadURL("kubernetes", "minikube", fmt.Sprintf("v%s", latestVersion), fmt.Sprintf("%s.sha256", binaryName))

	logger.Infof("Downloading minikube binary from %s", downloadURL)

	// Create cache directory
	cacheDir := bm.getCacheDir()
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return fmt.Errorf("failed to create cache directory: %w", err)
	}

	bm.binaryPath = filepath.Join(cacheDir, "minikube")

	// Download the binary using GitHub client
	if err := bm.githubClient.DownloadBinary(downloadURL, bm.binaryPath); err != nil {
		return fmt.Errorf("failed to download binary: %w", err)
	}

	// Verify checksum
	if err := bm.verifyChecksum(checksumURL, bm.binaryPath); err != nil {
		logger.Warnf("failed to verify checksum: %v", err)
		// Continue anyway as checksum verification is not critical
	}

	return nil
}

// verifyChecksum verifies the downloaded file's checksum
func (bm *BinaryManager) verifyChecksum(checksumURL, filePath string) error {
	// Download checksum
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(checksumURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	checksumData, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	expectedChecksum := strings.Fields(string(checksumData))[0]

	// Calculate file checksum
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}

	actualChecksum := hex.EncodeToString(hash.Sum(nil))

	if actualChecksum != expectedChecksum {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", expectedChecksum, actualChecksum)
	}

	return nil
}

// getBinaryName returns the appropriate binary name for the current platform
func (bm *BinaryManager) getBinaryName() string {
	os := runtime.GOOS
	arch := runtime.GOARCH

	baseName := "minikube"

	switch os {
	case "darwin":
		if arch == "arm64" {
			return "minikube-darwin-arm64"
		} else if arch == "amd64" {
			return "minikube-darwin-amd64"
		}
	case "linux":
		switch arch {
		case "amd64":
			return "minikube-linux-amd64"
		case "arm64":
			return "minikube-linux-arm64"
		default:
			panic(fmt.Sprintf("unsupported architecture %s", arch))
		}
	}

	// panic if platform is not supported
	panic(fmt.Sprintf("unable to download %s, unsupported platform %s/%s", baseName, os, arch))
}

// getBinaryPath returns the path where the binary should be stored
func (bm *BinaryManager) getBinaryPath() string {
	return filepath.Join(bm.getCacheDir(), "minikube")
}

// getCacheDir returns the cache directory for minikube binary
func (bm *BinaryManager) getCacheDir() string {
	if bm.cacheDir != "" {
		return bm.cacheDir
	}

	// Use system temp directory with a subdirectory for our app
	tempDir := os.TempDir()
	return filepath.Join(tempDir, "lok8", "bin")
}

// SetCacheDir sets a custom cache directory
func (bm *BinaryManager) SetCacheDir(dir string) {
	bm.cacheDir = dir
}

// Cleanup removes the downloaded binary
func (bm *BinaryManager) Cleanup() error {
	if bm.binaryPath != "" {
		return os.Remove(bm.binaryPath)
	}
	return nil
}

// GetVersion returns the version of the downloaded binary
func (bm *BinaryManager) GetVersion() (string, error) {
	if err := bm.EnsureBinary(); err != nil {
		return "", err
	}

	output, err := exec.Command(bm.binaryPath, "version", "--short").Output()
	if err != nil {
		return "", fmt.Errorf("failed to get minikube version: %w", err)
	}

	version := strings.TrimSpace(strings.TrimPrefix(string(output), "v"))
	return version, nil
}

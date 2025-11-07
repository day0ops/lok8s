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

package github

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/day0ops/lok8s/pkg/logger"
)

// GitHubRelease represents a GitHub release
type GitHubRelease struct {
	TagName string `json:"tag_name"`
	Name    string `json:"name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// GitHubClient handles GitHub API interactions
type GitHubClient struct {
	client  *http.Client
	baseURL string
}

// NewGitHubClient creates a new GitHub client
func NewGitHubClient() *GitHubClient {
	return &GitHubClient{
		client:  &http.Client{Timeout: 30 * time.Second}, // increased timeout for API calls
		baseURL: "https://api.github.com",
	}
}

// GetLatestRelease fetches the latest release for a given repository
func (gc *GitHubClient) GetLatestRelease(owner, repo string) (*GitHubRelease, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases/latest", gc.baseURL, owner, repo)

	logger.Debugf("fetching latest release from: %s", url)
	resp, err := gc.client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch latest release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch latest release: HTTP %d", resp.StatusCode)
	}

	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("failed to decode release response: %w", err)
	}

	logger.Debugf("fetched latest release: %s", release.TagName)
	return &release, nil
}

// GetLatestVersion fetches the latest version tag for a given repository
func (gc *GitHubClient) GetLatestVersion(owner, repo string) (string, error) {
	release, err := gc.GetLatestRelease(owner, repo)
	if err != nil {
		return "", err
	}

	// Remove 'v' prefix if present
	version := strings.TrimPrefix(release.TagName, "v")
	return version, nil
}

// GetBinaryDownloadURL constructs the download URL for a binary asset
func (gc *GitHubClient) GetBinaryDownloadURL(owner, repo, version, binaryName string) string {
	return fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/%s", owner, repo, version, binaryName)
}

// DownloadBinary downloads a binary from GitHub releases with retry logic
func (gc *GitHubClient) DownloadBinary(downloadURL, outputPath string) error {
	logger.Debugf("downloading binary from: %s to: %s", downloadURL, outputPath)

	// Use a longer timeout for binary downloads (5 minutes for large files)
	downloadClient := &http.Client{Timeout: 5 * time.Minute}

	maxRetries := 3
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		if attempt > 1 {
			backoff := time.Duration(attempt-1) * 2 * time.Second
			logger.Debugf("retrying download (attempt %d/%d) after %v...", attempt, maxRetries, backoff)
			time.Sleep(backoff)
		}

		resp, err := downloadClient.Get(downloadURL)
		if err != nil {
			lastErr = fmt.Errorf("failed to download binary: %w", err)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastErr = fmt.Errorf("failed to download binary: HTTP %d", resp.StatusCode)
			continue
		}

		// Create the output file
		file, err := os.Create(outputPath)
		if err != nil {
			resp.Body.Close()
			lastErr = fmt.Errorf("failed to create output file: %w", err)
			continue
		}

		// Copy the response body to the file
		_, err = io.Copy(file, resp.Body)
		file.Close()
		resp.Body.Close()

		if err != nil {
			// Clean up partial file on error
			os.Remove(outputPath)
			lastErr = fmt.Errorf("failed to write binary to file: %w", err)
			continue
		}

		logger.Debugf("binary download successful")
		return nil
	}

	return fmt.Errorf("failed to download binary after %d attempts: %w", maxRetries, lastErr)
}

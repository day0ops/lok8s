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

package services

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/day0ops/lok8s/pkg/config"
	"github.com/day0ops/lok8s/pkg/logger"
	"github.com/day0ops/lok8s/pkg/util/docker"
	"github.com/day0ops/lok8s/pkg/util/github"
	"github.com/day0ops/lok8s/pkg/util/k8s"
)

// CloudProviderKindManager manages cloud-provider-kind installation and operation
type CloudProviderKindManager struct {
	githubClient *github.GitHubClient
	processCache *ProcessCache
	testVersion  string // for testing purposes
}

// CloudProviderProcess represents a running cloud-provider-kind process
type CloudProviderProcess struct {
	PID         int    `json:"pid"`
	ContextName string `json:"context_name"`
	TempDir     string `json:"temp_dir"`
	LogDir      string `json:"log_dir"`
	BinaryPath  string `json:"binary_path"`
	StartTime   string `json:"start_time"`
}

// ProcessCache manages cloud-provider-kind process tracking
type ProcessCache struct {
	Processes map[string]CloudProviderProcess `json:"processes"`
	CacheFile string                          `json:"-"`
}

// NewCloudProviderKindManager creates a new cloud-provider-kind manager
func NewCloudProviderKindManager() *CloudProviderKindManager {
	return &CloudProviderKindManager{
		githubClient: github.NewGitHubClient(),
		processCache: newProcessCache(),
		testVersion:  "", // empty means use latest
	}
}

// SetTestVersion sets a specific version for testing purposes
func (cpkm *CloudProviderKindManager) SetTestVersion(version string) {
	cpkm.testVersion = version
	logger.Debugf("set test version to: %s", version)
}

// newProcessCache creates a new process cache
func newProcessCache() *ProcessCache {
	cacheDir := filepath.Join(os.Getenv("HOME"), ".lok8")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		logger.Warnf("failed to create cache directory: %v", err)
	}

	return &ProcessCache{
		Processes: make(map[string]CloudProviderProcess),
		CacheFile: filepath.Join(cacheDir, "cloud-provider-processes.json"),
	}
}

// loadProcessCache loads the process cache from disk
func (pc *ProcessCache) loadProcessCache() error {
	if _, err := os.Stat(pc.CacheFile); os.IsNotExist(err) {
		pc.Processes = make(map[string]CloudProviderProcess)
		return nil
	}

	data, err := os.ReadFile(pc.CacheFile)
	if err != nil {
		return fmt.Errorf("failed to read process cache: %w", err)
	}

	if err := json.Unmarshal(data, pc); err != nil {
		return fmt.Errorf("failed to unmarshal process cache: %w", err)
	}

	return nil
}

// saveProcessCache saves the process cache to disk
func (pc *ProcessCache) saveProcessCache() error {
	data, err := json.MarshalIndent(pc, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal process cache: %w", err)
	}

	if err := os.WriteFile(pc.CacheFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write process cache: %w", err)
	}

	return nil
}

// addProcess adds a process to the cache
func (pc *ProcessCache) addProcess(contextName string, process CloudProviderProcess) error {
	if err := pc.loadProcessCache(); err != nil {
		logger.Warnf("failed to load process cache: %v", err)
		pc.Processes = make(map[string]CloudProviderProcess)
	}

	pc.Processes[contextName] = process
	return pc.saveProcessCache()
}

// getProcess retrieves a process from the cache
func (pc *ProcessCache) getProcess(contextName string) (CloudProviderProcess, bool) {
	if err := pc.loadProcessCache(); err != nil {
		logger.Warnf("failed to load process cache: %v", err)
		return CloudProviderProcess{}, false
	}

	process, exists := pc.Processes[contextName]
	return process, exists
}

// terminateProcess safely terminates a cloud-provider-kind process
func (pc *ProcessCache) terminateProcess(contextName string) error {
	process, exists := pc.getProcess(contextName)
	if !exists {
		logger.Debugf("no cloud-provider-kind process found for context %s", contextName)
		return nil
	}

	logger.Infof("🚨 terminating cloud-provider-kind process for context %s (PID: %d)", contextName, process.PID)

	// terminate the process
	if err := syscall.Kill(process.PID, syscall.SIGTERM); err != nil {
		logger.Warnf("failed to terminate process %d: %v", process.PID, err)
		// try SIGKILL as fallback
		if err := syscall.Kill(process.PID, syscall.SIGKILL); err != nil {
			logger.Warnf("failed to kill process %d: %v", process.PID, err)
		}
	}

	// clean up temp directory
	if process.TempDir != "" {
		if err := os.RemoveAll(process.TempDir); err != nil {
			logger.Warnf("failed to remove temp directory %s: %v", process.TempDir, err)
		} else {
			logger.Debugf("cleaned up temp directory: %s", process.TempDir)
		}
	}

	// remove from cache
	if err := pc.loadProcessCache(); err == nil {
		delete(pc.Processes, contextName)
		if err := pc.saveProcessCache(); err != nil {
			logger.Warnf("failed to save process cache: %v", err)
		}
	}

	logger.Infof("successfully terminated cloud-provider-kind process for context %s", contextName)
	return nil
}

// Install installs and runs cloud-provider-kind as a background process
func (cpkm *CloudProviderKindManager) Install(contextName string, skipOsCheck bool) error {
	if config.IsDarwin() && !skipOsCheck {
		logger.Warnf("⚠️ skipping installing tunnel on macOS as it requires privileges to create the port mapping")
		logger.Warnf("⚠️ install on macOS using 'sudo %s kind-tunnel' command instead)", config.AppName)
		logger.Warnf("⚠️ look at '%s kind-tunnel -h' for help", config.AppName)
		return nil
	}

	status := logger.NewStatus()
	status.Start(fmt.Sprintf("installing cloud-provider-kind for context %s", contextName))
	defer func() {
		if status != nil {
			status.End(true)
		}
	}()

	// create temp directory for cloud-provider-kind
	tempDir, err := os.MkdirTemp("", "cloud-provider-kind-*")
	if err != nil {
		status.End(false)
		return fmt.Errorf("failed to create temp directory: %w", err)
	}

	// download cloud-provider-kind binary
	binaryPath := filepath.Join(tempDir, "cloud-provider-kind")
	if err := cpkm.downloadBinary(binaryPath); err != nil {
		status.End(false)
		os.RemoveAll(tempDir) // cleanup on failure
		return fmt.Errorf("failed to download cloud-provider-kind: %w", err)
	}

	// make binary executable
	if err := os.Chmod(binaryPath, 0755); err != nil {
		status.End(false)
		os.RemoveAll(tempDir) // cleanup on failure
		return fmt.Errorf("failed to make cloud-provider-kind executable: %w", err)
	}

	// start cloud-provider-kind as background process
	if err := cpkm.startProcess(binaryPath, contextName, tempDir); err != nil {
		status.End(false)
		return fmt.Errorf("failed to start cloud-provider-kind: %w", err)
	}

	// Success - status.End(true) will be called by defer
	return nil
}

// downloadBinary downloads the cloud-provider-kind binary with checksum verification
func (cpkm *CloudProviderKindManager) downloadBinary(binaryPath string) error {
	logger.Debugf("downloading cloud-provider-kind binary to %s", binaryPath)

	// get version (use test version if set, otherwise get latest)
	var version string
	var err error

	if cpkm.testVersion != "" {
		version = cpkm.testVersion
		logger.Debugf("using test version: %s", version)
	} else {
		version, err = cpkm.githubClient.GetLatestVersion("kubernetes-sigs", "cloud-provider-kind")
		if err != nil {
			logger.Warnf("failed to get latest cloud-provider-kind version, using default: %v", err)
			version = config.CloudProviderKindMinSupportedVersion // fallback to known working version
		}
	}

	// construct binary name
	binaryName := getBinaryName(version)

	// construct download URL
	downloadURL := cpkm.githubClient.GetBinaryDownloadURL("kubernetes-sigs", "cloud-provider-kind", "v"+version, binaryName)

	logger.Debugf("downloading cloud-provider-kind from: %s", downloadURL)

	// download the tar.gz file to a temporary location
	tempArchivePath := binaryPath + ".tar.gz"
	if err := cpkm.githubClient.DownloadBinary(downloadURL, tempArchivePath); err != nil {
		return fmt.Errorf("failed to download cloud-provider-kind from %s: %w", downloadURL, err)
	}

	// verify checksum of the downloaded archive
	if err := cpkm.verifyChecksum(tempArchivePath, version, binaryName); err != nil {
		os.Remove(tempArchivePath) // cleanup on checksum failure
		return fmt.Errorf("checksum verification failed: %w", err)
	}

	// extract the binary from the tar.gz archive
	if err := cpkm.extractBinary(tempArchivePath, binaryPath); err != nil {
		os.Remove(tempArchivePath) // cleanup on extraction failure
		return fmt.Errorf("failed to extract binary from archive: %w", err)
	}

	// cleanup the temporary archive file
	os.Remove(tempArchivePath)

	logger.Debugf("downloaded, verified and extracted cloud-provider-kind binary")
	return nil
}

// extractBinary extracts the binary from a tar.gz archive
func (cpkm *CloudProviderKindManager) extractBinary(archivePath, binaryPath string) error {
	logger.Debugf("extracting binary from %s to %s", archivePath, binaryPath)

	// open the tar.gz file
	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("failed to open archive file: %w", err)
	}
	defer file.Close()

	// create gzip reader
	gzReader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzReader.Close()

	// create tar reader
	tarReader := tar.NewReader(gzReader)

	// extract files from the archive
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break // end of archive
		}
		if err != nil {
			return fmt.Errorf("failed to read tar header: %w", err)
		}

		// look for the binary file (should be named "cloud-provider-kind")
		if header.Name == "cloud-provider-kind" {
			// create the output file
			outFile, err := os.Create(binaryPath)
			if err != nil {
				return fmt.Errorf("failed to create output file: %w", err)
			}
			defer outFile.Close()

			// copy the file content
			_, err = io.Copy(outFile, tarReader)
			if err != nil {
				return fmt.Errorf("failed to extract binary: %w", err)
			}

			logger.Debugf("successfully extracted cloud-provider-kind binary")
			return nil
		}
	}

	return fmt.Errorf("cloud-provider-kind binary not found in archive")
}

// startProcess starts cloud-provider-kind as a background process
func (cpkm *CloudProviderKindManager) startProcess(binaryPath, contextName, tempDir string) error {
	logger.Infof("starting cloud-provider-kind for context %s", contextName)

	// create temporary log directory
	logDir, err := os.MkdirTemp("", fmt.Sprintf("cloud-provider-kind-logs-%s", contextName))
	if err != nil {
		return fmt.Errorf("failed to create log directory: %w", err)
	}

	cmd := exec.Command(binaryPath, "--enable-lb-port-mapping", "--enable-log-dumping", "--logs-dir", logDir)

	// set environment variables
	path, err := k8s.GetKubeConfigPath()
	if err != nil {
		return err
	}
	cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", path))

	// cloud-provider-kind uses the standard Docker SDK, which needs a real
	// DOCKER_HOST. Podman machine's connections are SSH tunnels the podman
	// CLI knows how to use but the Docker SDK can't, and podman machine's
	// own local socket (what the Docker SDK needs instead) isn't guaranteed
	// to match whatever DOCKER_HOST (if any) happens to be in our own
	// environment - so resolve it explicitly rather than relying on
	// inheritance.
	if runtime, err := docker.GetContainerRuntime(); err == nil && runtime == "podman" && config.IsDarwin() {
		if dockerHost, err := resolvePodmanMachineDockerHost(); err != nil {
			logger.Warnf("failed to resolve podman machine socket for DOCKER_HOST: %v", err)
		} else {
			cmd.Env = append(cmd.Env, fmt.Sprintf("DOCKER_HOST=%s", dockerHost))
		}
	}

	// redirect stdout and stderr to /dev/null to avoid zombie processes
	//cmd.Stderr = os.Stderr
	//cmd.Stdout = os.Stdout
	cmd.Stdin = os.Stdin

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}

	// start the process in background
	if err = cmd.Start(); err != nil {
		return fmt.Errorf("failed to start cloud-provider-kind process: %w", err)
	}

	// add process to cache
	process := CloudProviderProcess{
		PID:         cmd.Process.Pid,
		ContextName: contextName,
		TempDir:     tempDir,
		LogDir:      logDir,
		BinaryPath:  binaryPath,
		StartTime:   fmt.Sprintf("%d", cmd.Process.Pid), // simple timestamp placeholder
	}
	if err := cpkm.processCache.addProcess(contextName, process); err != nil {
		logger.Warnf("failed to add process to cache: %v", err)
	}

	// a successful fork only means the OS started the process, not that it
	// kept running - e.g. a bad flag makes it exit immediately. Give it a
	// moment to fail fast, then actually check it's still alive.
	time.Sleep(500 * time.Millisecond)
	if err := cpkm.verifyProcessRunning(cmd.Process.Pid); err != nil {
		if termErr := cpkm.processCache.terminateProcess(contextName); termErr != nil {
			logger.Warnf("failed to clean up dead process entry: %v", termErr)
		}
		return fmt.Errorf("cloud-provider-kind exited immediately after starting, check logs in %s: %w", logDir, err)
	}

	logger.Infof("✓ started cloud-provider-kind process (PID: %d) for context %s", cmd.Process.Pid, contextName)

	return nil
}

// podmanMachineCommand builds a `podman machine ...` command. podman
// refuses to run "machine" subcommands as root ("cannot run command as
// root"), but we're root here when launched via sudo on macOS - so run as
// the invoking user instead, via the SUDO_USER sudo itself always sets.
func podmanMachineCommand(args ...string) *exec.Cmd {
	if os.Geteuid() == 0 {
		if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" {
			return exec.Command("sudo", append([]string{"-u", sudoUser, "podman"}, args...)...)
		}
	}
	return exec.Command("podman", args...)
}

// resolvePodmanMachineDockerHost returns a DOCKER_HOST value pointing at the
// currently running podman machine's local API socket.
func resolvePodmanMachineDockerHost() (string, error) {
	out, err := podmanMachineCommand("machine", "list", "--format", "json").Output()
	if err != nil {
		return "", fmt.Errorf("failed to list podman machines: %w", err)
	}

	var machines []struct {
		Name    string `json:"Name"`
		Running bool   `json:"Running"`
	}
	if err := json.Unmarshal(out, &machines); err != nil {
		return "", fmt.Errorf("failed to parse podman machine list: %w", err)
	}

	var activeMachine string
	for _, m := range machines {
		if m.Running {
			activeMachine = m.Name
			break
		}
	}
	if activeMachine == "" {
		return "", fmt.Errorf("no running podman machine found")
	}

	out, err = podmanMachineCommand("machine", "inspect", activeMachine, "--format", "{{.ConnectionInfo.PodmanSocket.Path}}").Output()
	if err != nil {
		return "", fmt.Errorf("failed to inspect podman machine %s: %w", activeMachine, err)
	}

	return "unix://" + strings.TrimSpace(string(out)), nil
}

// verifyProcessRunning checks whether pid is still alive via signal 0.
// os.FindProcess alone can't tell us this: on POSIX it always succeeds
// regardless of whether the process actually exists.
func (cpkm *CloudProviderKindManager) verifyProcessRunning(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("process %d not found: %w", pid, err)
	}
	if err := process.Signal(syscall.Signal(0)); err != nil {
		return fmt.Errorf("process %d is not running: %w", pid, err)
	}
	logger.Debugf("verified process %d is running", pid)
	return nil
}

// HasExistingProcesses checks if there are any existing cloud-provider-kind processes in the cache
func (cpkm *CloudProviderKindManager) HasExistingProcesses() (bool, []CloudProviderProcess, error) {
	if err := cpkm.processCache.loadProcessCache(); err != nil {
		logger.Debugf("failed to load process cache: %v", err)
		return false, nil, nil
	}

	var processes []CloudProviderProcess
	for contextName, process := range cpkm.processCache.Processes {
		processes = append(processes, process)
		logger.Debugf("found cloud-provider-kind process entry for context %s (PID: %d)", contextName, process.PID)
	}

	return len(processes) > 0, processes, nil
}

// Terminate terminates a cloud-provider-kind process for the given context
func (cpkm *CloudProviderKindManager) Terminate(contextName string, skipOsCheck bool) error {
	if config.IsDarwin() && !skipOsCheck {
		logger.Warnf("⚠️ skipping terminating of cloud-provider-kind process on macOS")
		logger.Warnf("⚠️ on macOS, terminate the processes using 'sudo %s kind-tunnel -d' command instead", config.AppName)
		logger.Warnf("⚠️ look at '%s kind-tunnel -h' for help", config.AppName)
		return nil
	}

	return cpkm.processCache.terminateProcess(contextName)
}

// verifyChecksum verifies the SHA256 checksum of the downloaded binary
func (cpkm *CloudProviderKindManager) verifyChecksum(binaryPath, version, binaryName string) error {
	logger.Debugf("verifying checksum for %s", binaryPath)

	// fetch checksums from GitHub
	expectedChecksum, err := cpkm.fetchExpectedChecksum(version)
	if err != nil {
		return fmt.Errorf("failed to fetch expected checksum: %w", err)
	}

	// calculate actual checksum
	actualChecksum, err := cpkm.calculateFileChecksum(binaryPath)
	if err != nil {
		return fmt.Errorf("failed to calculate file checksum: %w", err)
	}

	// compare checksums
	if actualChecksum != expectedChecksum {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", expectedChecksum, actualChecksum)
	}

	logger.Debugf("checksum verification passed")
	return nil
}

// fetchExpectedChecksum fetches the expected SHA256 checksum from GitHub releases
func (cpkm *CloudProviderKindManager) fetchExpectedChecksum(version string) (string, error) {
	// construct checksums URL
	checksumsURL := fmt.Sprintf("https://github.com/kubernetes-sigs/cloud-provider-kind/releases/download/v%s/cloud-provider-kind_%s_checksums.txt", version, version)

	logger.Debugf("fetching checksums from: %s", checksumsURL)

	// fetch checksums file
	resp, err := http.Get(checksumsURL)
	if err != nil {
		return "", fmt.Errorf("failed to fetch checksums file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to fetch checksums file, status: %d", resp.StatusCode)
	}

	// read checksums content
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read checksums content: %w", err)
	}

	// parse checksums and find the one for our binary
	checksums := string(body)
	lines := strings.Split(checksums, "\n")

	// construct expected filename for checksum lookup
	expectedFilename := fmt.Sprintf("cloud-provider-kind_%s_%s_%s.tar.gz", version, runtime.GOOS, runtime.GOARCH)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// checksum format: "hash filename"
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			checksum := parts[0]
			filename := parts[1]

			if filename == expectedFilename {
				logger.Debugf("found expected checksum for %s: %s", filename, checksum)
				return checksum, nil
			}
		}
	}

	return "", fmt.Errorf("checksum not found for binary %s", expectedFilename)
}

// calculateFileChecksum calculates the SHA256 checksum of a file
func (cpkm *CloudProviderKindManager) calculateFileChecksum(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("failed to calculate hash: %w", err)
	}

	checksum := hex.EncodeToString(hash.Sum(nil))
	logger.Debugf("calculated checksum for %s: %s", filePath, checksum)
	return checksum, nil
}

// getBinaryName constructs the appropriate binary name for the current platform
func getBinaryName(version string) string {
	os := runtime.GOOS
	arch := runtime.GOARCH

	baseName := "cloud-provider-kind"

	switch os {
	case "darwin":
		if arch == "arm64" {
			return fmt.Sprintf("%s_%s_darwin_arm64.tar.gz", baseName, version)
		} else if arch == "amd64" {
			return fmt.Sprintf("%s_%s_darwin_amd64.tar.gz", baseName, version)
		}
	case "linux":
		if arch == "arm64" {
			return fmt.Sprintf("%s_%s_linux_arm64.tar.gz", baseName, version)
		} else if arch == "amd64" {
			return fmt.Sprintf("%s_%s_linux_amd64.tar.gz", baseName, version)
		}
	}

	// panic if platform is not supported
	panic(fmt.Sprintf("unable to download %s, unsupported platform %s/%s", baseName, os, arch))
}

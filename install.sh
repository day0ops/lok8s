#!/bin/bash

set -e

# Parse command line arguments
TARGET="${1:-latest}"  # Optional target parameter (latest, stable, or specific version like v1.0.0)

# GitHub repository information
REPO_OWNER="day0ops"
REPO_NAME="lok8s"
BINARY_NAME="lok8s"

# Determine install directory
if [ -w "/usr/local/bin" ]; then
    INSTALL_DIR="/usr/local/bin"
elif [ -w "$HOME/.local/bin" ]; then
    INSTALL_DIR="$HOME/.local/bin"
    # Create directory if it doesn't exist
    mkdir -p "$INSTALL_DIR"
else
    # Default to /usr/local/bin (will require sudo)
    INSTALL_DIR="/usr/local/bin"
fi

# Validate target if provided
if [[ -n "$TARGET" ]] && [[ ! "$TARGET" =~ ^(stable|latest|[vV]?[0-9]+\.[0-9]+\.[0-9]+(-[^[:space:]]+)?)$ ]]; then
    echo "Usage: $0 [stable|latest|VERSION]" >&2
    echo "  stable - Install the latest stable release" >&2
    echo "  latest - Install the latest release (including prereleases)" >&2
    echo "  VERSION - Install a specific version (e.g., v1.0.0)" >&2
    exit 1
fi

# Check for required dependencies
DOWNLOADER=""
if command -v curl >/dev/null 2>&1; then
    DOWNLOADER="curl"
elif command -v wget >/dev/null 2>&1; then
    DOWNLOADER="wget"
else
    echo "Error: Either curl or wget is required but neither is installed" >&2
    exit 1
fi

# Check if jq is available (optional)
HAS_JQ=false
if command -v jq >/dev/null 2>&1; then
    HAS_JQ=true
fi

# Download function that works with both curl and wget
download_file() {
    local url="$1"
    local output="$2"
    
    if [ "$DOWNLOADER" = "curl" ]; then
        if [ -n "$output" ]; then
            curl -fsSL -o "$output" "$url"
        else
            curl -fsSL "$url"
        fi
    elif [ "$DOWNLOADER" = "wget" ]; then
        if [ -n "$output" ]; then
            wget -q -O "$output" "$url"
        else
            wget -q -O - "$url"
        fi
    else
        return 1
    fi
}

# Get latest release version from GitHub API
get_latest_release() {
    local include_prerelease="$1"
    local api_url="https://api.github.com/repos/${REPO_OWNER}/${REPO_NAME}/releases"
    
    if [ "$include_prerelease" = "true" ]; then
        api_url="${api_url}?per_page=1"
    else
        api_url="${api_url}/latest"
    fi
    
    if [ "$HAS_JQ" = true ]; then
        if [ "$include_prerelease" = "true" ]; then
            download_file "$api_url" | jq -r '.[0].tag_name // empty'
        else
            download_file "$api_url" | jq -r '.tag_name // empty'
        fi
    else
        # Fallback to simple grep/sed parsing
        if [ "$include_prerelease" = "true" ]; then
            download_file "$api_url" | grep -o '"tag_name":"[^"]*"' | head -1 | sed 's/"tag_name":"\([^"]*\)"/\1/'
        else
            download_file "$api_url" | grep -o '"tag_name":"[^"]*"' | head -1 | sed 's/"tag_name":"\([^"]*\)"/\1/'
        fi
    fi
}

# Detect platform
case "$(uname -s)" in
    Darwin) os="darwin" ;;
    Linux) os="linux" ;;
    *) echo "Error: Unsupported OS: $(uname -s)" >&2; exit 1 ;;
esac

case "$(uname -m)" in
    x86_64|amd64) arch="amd64" ;;
    arm64|aarch64) arch="arm64" ;;
    *) echo "Error: Unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

platform="${os}-${arch}"
binary_file="${BINARY_NAME}-${platform}"
checksum_file="${BINARY_NAME}-${platform}.sha256sum"

# Determine version to install
VERSION=""
if [ "$TARGET" = "latest" ]; then
    echo "Fetching latest release..."
    VERSION=$(get_latest_release "true")
elif [ "$TARGET" = "stable" ]; then
    echo "Fetching latest stable release..."
    VERSION=$(get_latest_release "false")
else
    # Normalize version (add 'v' prefix if not present)
    if [[ ! "$TARGET" =~ ^[vV] ]]; then
        VERSION="v${TARGET}"
    else
        VERSION="$TARGET"
    fi
fi

if [ -z "$VERSION" ]; then
    echo "Error: Failed to determine version to install" >&2
    exit 1
fi

echo "Installing ${BINARY_NAME} ${VERSION} for ${platform}..."

# GitHub release URLs
GITHUB_BASE_URL="https://github.com/${REPO_OWNER}/${REPO_NAME}/releases/download/${VERSION}"
BINARY_URL="${GITHUB_BASE_URL}/${binary_file}"
CHECKSUM_URL="${GITHUB_BASE_URL}/${checksum_file}"

# Create temporary directory
TEMP_DIR=$(mktemp -d)
trap "rm -rf $TEMP_DIR" EXIT

# Download binary and checksum
echo "Downloading binary..."
if ! download_file "$BINARY_URL" "${TEMP_DIR}/${binary_file}"; then
    echo "Error: Failed to download binary from ${BINARY_URL}" >&2
    exit 1
fi

echo "Downloading checksum..."
if ! download_file "$CHECKSUM_URL" "${TEMP_DIR}/${checksum_file}"; then
    echo "Error: Failed to download checksum from ${CHECKSUM_URL}" >&2
    exit 1
fi

# Verify checksum
echo "Verifying checksum..."
if command -v openssl >/dev/null 2>&1; then
    actual=$(openssl sha256 -r "${TEMP_DIR}/${binary_file}" | awk '{print $1}' | tr -d '*')
else
    if [ "$os" = "darwin" ]; then
        actual=$(shasum -a 256 "${TEMP_DIR}/${binary_file}" | awk '{print $1}')
    else
        actual=$(sha256sum "${TEMP_DIR}/${binary_file}" | awk '{print $1}')
    fi
fi

# Extract expected checksum from file (handle both openssl and shasum formats)
# openssl format: "hash *filename" or "hash filename"
# shasum format: "hash filename"
expected=$(cat "${TEMP_DIR}/${checksum_file}" | awk '{print $1}' | tr -d '*')

if [ "$actual" != "$expected" ]; then
    echo "Error: Checksum verification failed" >&2
    echo "  Expected: $expected" >&2
    echo "  Got:      $actual" >&2
    echo "" >&2
    echo "Debug info:" >&2
    echo "  Checksum file content: $(cat "${TEMP_DIR}/${checksum_file}")" >&2
    echo "  Binary file size: $(stat -f%z "${TEMP_DIR}/${binary_file}" 2>/dev/null || stat -c%s "${TEMP_DIR}/${binary_file}" 2>/dev/null)" >&2
    exit 1
fi

echo "Checksum verified successfully"

# Make binary executable
chmod +x "${TEMP_DIR}/${binary_file}"

# Check if we need sudo for installation
NEED_SUDO=false
if [ ! -w "$INSTALL_DIR" ]; then
    NEED_SUDO=true
fi

# Install binary
echo "Installing ${BINARY_NAME} to ${INSTALL_DIR}..."
if [ "$NEED_SUDO" = true ]; then
    sudo cp "${TEMP_DIR}/${binary_file}" "${INSTALL_DIR}/${BINARY_NAME}"
    sudo chmod +x "${INSTALL_DIR}/${BINARY_NAME}"
else
    cp "${TEMP_DIR}/${binary_file}" "${INSTALL_DIR}/${BINARY_NAME}"
    chmod +x "${INSTALL_DIR}/${BINARY_NAME}"
fi

# Verify installation
if command -v "${BINARY_NAME}" >/dev/null 2>&1; then
    INSTALLED_VERSION=$("${BINARY_NAME}" version 2>/dev/null | head -1 || echo "unknown")
    echo ""
    echo "✅ Installation complete!"
    echo ""
    echo "  ${BINARY_NAME} has been installed to ${INSTALL_DIR}/${BINARY_NAME}"
    echo "  Version: ${INSTALLED_VERSION}"
    echo ""
    echo "  Run '${BINARY_NAME} --help' to get started"
else
    echo ""
    echo "⚠️  Installation completed, but ${BINARY_NAME} is not in PATH"
    echo "  Binary installed to: ${INSTALL_DIR}/${BINARY_NAME}"
    echo "  Please ensure ${INSTALL_DIR} is in your PATH"
fi


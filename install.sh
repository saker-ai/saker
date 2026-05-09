#!/bin/bash

set -e

REPO="cinience/saker"
BINARY_NAME="saker"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"

# Parse command line arguments
VERSION="$1"

# Validate version if provided
if [[ -n "$VERSION" ]] && [[ ! "$VERSION" =~ ^v?[0-9]+\.[0-9]+\.[0-9]+(-[^[:space:]]+)?$ ]]; then
    echo "Usage: $0 [VERSION]" >&2
    echo "  VERSION: e.g. v0.1.0 or 0.1.0 (default: latest)" >&2
    exit 1
fi

# Ensure version has 'v' prefix
if [[ -n "$VERSION" ]] && [[ ! "$VERSION" =~ ^v ]]; then
    VERSION="v${VERSION}"
fi

# Check for required dependencies
DOWNLOADER=""
if command -v curl >/dev/null 2>&1; then
    DOWNLOADER="curl"
elif command -v wget >/dev/null 2>&1; then
    DOWNLOADER="wget"
else
    echo "Error: curl or wget is required but neither is installed." >&2
    exit 1
fi

if ! command -v tar >/dev/null 2>&1; then
    echo "Error: tar is required but not installed." >&2
    exit 1
fi

# Download helper
download_file() {
    local url="$1"
    local output="$2"

    if [ "$DOWNLOADER" = "curl" ]; then
        if [ -n "$output" ]; then
            curl -fsSL -o "$output" "$url"
        else
            curl -fsSL "$url"
        fi
    else
        if [ -n "$output" ]; then
            wget -q -O "$output" "$url"
        else
            wget -q -O - "$url"
        fi
    fi
}

# Detect OS
case "$(uname -s)" in
    Darwin)  os="darwin" ;;
    Linux)   os="linux" ;;
    MINGW*|MSYS*|CYGWIN*)
        echo "Windows is not supported by this script." >&2
        echo "Please download manually from: https://github.com/${REPO}/releases" >&2
        exit 1
        ;;
    *)
        echo "Unsupported operating system: $(uname -s)" >&2
        exit 1
        ;;
esac

# Detect architecture
case "$(uname -m)" in
    x86_64|amd64)  arch="amd64" ;;
    arm64|aarch64) arch="arm64" ;;
    *)
        echo "Unsupported architecture: $(uname -m)" >&2
        exit 1
        ;;
esac

# Detect Rosetta 2 on macOS
if [ "$os" = "darwin" ] && [ "$arch" = "amd64" ]; then
    if [ "$(sysctl -n sysctl.proc_translated 2>/dev/null)" = "1" ]; then
        arch="arm64"
    fi
fi

platform="${os}-${arch}"
echo "Detected platform: ${platform}"

# Resolve version
if [ -z "$VERSION" ]; then
    echo "Fetching latest release..."
    if [ "$DOWNLOADER" = "curl" ]; then
        VERSION=$(curl -fsSL -o /dev/null -w '%{redirect_url}' "https://github.com/${REPO}/releases/latest" | grep -oE 'v[0-9]+\.[0-9]+\.[0-9]+[^/]*')
    else
        VERSION=$(wget --spider --max-redirect=0 "https://github.com/${REPO}/releases/latest" 2>&1 | grep -i 'Location' | grep -oE 'v[0-9]+\.[0-9]+\.[0-9]+[^/]*')
    fi

    if [ -z "$VERSION" ]; then
        echo "Error: failed to determine latest version." >&2
        echo "Please specify a version: $0 v0.1.0" >&2
        exit 1
    fi
fi

echo "Installing ${BINARY_NAME} ${VERSION} ..."

# Build download URL
archive="${BINARY_NAME}-${VERSION}-${platform}.tar.gz"
download_url="https://github.com/${REPO}/releases/download/${VERSION}/${archive}"

# Create temp directory
tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT

# Download
echo "Downloading ${download_url} ..."
if ! download_file "$download_url" "$tmpdir/$archive"; then
    echo "Error: download failed." >&2
    echo "Please check that version ${VERSION} exists and has a ${platform} binary:" >&2
    echo "  https://github.com/${REPO}/releases/tag/${VERSION}" >&2
    exit 1
fi

# Download checksums and verify integrity
checksums_url="https://github.com/${REPO}/releases/download/${VERSION}/checksums.txt"
echo "Verifying checksum..."
if ! download_file "$checksums_url" "$tmpdir/checksums.txt"; then
    echo "Warning: checksums file not available, skipping verification." >&2
else
    expected_sha=$(grep "$archive" "$tmpdir/checksums.txt" | awk '{print $1}')
    if [ -z "$expected_sha" ]; then
        echo "Warning: no checksum found for ${archive}, skipping verification." >&2
    else
        actual_sha=$(sha256sum "$tmpdir/$archive" | awk '{print $1}')
        if [ "$actual_sha" != "$expected_sha" ]; then
            echo "Error: checksum mismatch!" >&2
            echo "  Expected: $expected_sha" >&2
            echo "  Actual:   $actual_sha" >&2
            echo "The download may be corrupted or tampered with. Aborting." >&2
            exit 1
        fi
        echo "Checksum verified."
    fi
fi

# Extract
echo "Extracting..."
tar -xzf "$tmpdir/$archive" -C "$tmpdir"

# Find the binary
binary_path=""
if [ -f "$tmpdir/$BINARY_NAME" ]; then
    binary_path="$tmpdir/$BINARY_NAME"
elif [ -f "$tmpdir/${BINARY_NAME}-${VERSION}-${platform}/$BINARY_NAME" ]; then
    binary_path="$tmpdir/${BINARY_NAME}-${VERSION}-${platform}/$BINARY_NAME"
else
    # Search for it
    binary_path=$(find "$tmpdir" -name "$BINARY_NAME" -type f | head -1)
fi

if [ -z "$binary_path" ] || [ ! -f "$binary_path" ]; then
    echo "Error: could not find ${BINARY_NAME} binary in archive." >&2
    exit 1
fi

chmod +x "$binary_path"

# Install
mkdir -p "$INSTALL_DIR"
mv "$binary_path" "$INSTALL_DIR/$BINARY_NAME"

# Verify and hint PATH setup
echo ""
echo "Successfully installed ${BINARY_NAME} ${VERSION} to ${INSTALL_DIR}/${BINARY_NAME}"

if ! echo "$PATH" | tr ':' '\n' | grep -qx "$INSTALL_DIR"; then
    echo ""
    echo "Note: ${INSTALL_DIR} is not in your PATH."

    # Detect user shell config file
    shell_config=""
    case "$(basename "$SHELL")" in
        zsh)  shell_config="$HOME/.zshrc" ;;
        bash)
            if [ -f "$HOME/.bashrc" ]; then
                shell_config="$HOME/.bashrc"
            elif [ -f "$HOME/.bash_profile" ]; then
                shell_config="$HOME/.bash_profile"
            fi
            ;;
        fish) shell_config="$HOME/.config/fish/config.fish" ;;
    esac

    if [ -n "$shell_config" ]; then
        echo "Run this to add it:"
        echo ""
        echo "  echo 'export PATH=\"${INSTALL_DIR}:\$PATH\"' >> ${shell_config} && source ${shell_config}"
    else
        echo "Add it with: export PATH=\"${INSTALL_DIR}:\$PATH\""
    fi
else
    echo ""
    echo "Run 'saker --help' to get started."
fi

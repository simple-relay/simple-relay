#!/bin/sh
set -eu

# Simple Relay Standalone Installer
# Usage: curl -fsSL https://raw.githubusercontent.com/simple-relay/simple-relay/main/install.sh | sh

# 1. Colors & logging setup
setup_colors() {
  if [ -t 1 ]; then
    COLOR_RESET="\033[0m"
    COLOR_INFO="\033[1;34m"
    COLOR_SUCCESS="\033[1;32m"
    COLOR_WARN="\033[1;33m"
    COLOR_ERROR="\033[1;31m"
    COLOR_BOLD="\033[1m"
  else
    COLOR_RESET=""
    COLOR_INFO=""
    COLOR_SUCCESS=""
    COLOR_WARN=""
    COLOR_ERROR=""
    COLOR_BOLD=""
  fi
}

log_info() {
  printf "%b[INFO]%b %b\n" "${COLOR_INFO}" "${COLOR_RESET}" "$1"
}

log_success() {
  printf "%b[SUCCESS]%b %b\n" "${COLOR_SUCCESS}" "${COLOR_RESET}" "$1"
}

log_warn() {
  printf "%b[WARN]%b %b\n" "${COLOR_WARN}" "${COLOR_RESET}" "$1"
}

log_error() {
  printf "%b[ERROR]%b %b\n" "${COLOR_ERROR}" "${COLOR_RESET}" "$1" >&2
}

# 2. Detect operating system
detect_os() {
  OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
  case "$OS" in
    linux) OS="linux" ;;
    darwin) OS="darwin" ;;
    *)
      log_error "Unsupported operating system '$OS'. Simple Relay supports Linux and macOS."
      exit 1
      ;;
  esac
}

# 3. Detect architecture
detect_arch() {
  ARCH="$(uname -m | tr '[:upper:]' '[:lower:]')"
  case "$ARCH" in
    x86_64|amd64) ARCH="amd64" ;;
    arm64|aarch64) ARCH="arm64" ;;
    *)
      log_error "Unsupported architecture '$ARCH'. Simple Relay supports amd64 and arm64."
      exit 1
      ;;
  esac
}

# 4. Verify required dependencies
check_dependencies() {
  if command -v curl >/dev/null 2>&1; then
    DOWNLOADER="curl"
  elif command -v wget >/dev/null 2>&1; then
    DOWNLOADER="wget"
  else
    log_error "Neither 'curl' nor 'wget' was found. Please install curl or wget."
    exit 1
  fi

  if ! command -v tar >/dev/null 2>&1; then
    log_error "'tar' is required for archive extraction. Please install tar."
    exit 1
  fi

  if command -v sha256sum >/dev/null 2>&1; then
    CHECKSUM_TOOL="sha256sum"
  elif command -v shasum >/dev/null 2>&1; then
    CHECKSUM_TOOL="shasum"
  elif command -v openssl >/dev/null 2>&1; then
    CHECKSUM_TOOL="openssl"
  else
    log_error "No SHA256 verification tool found. Please install sha256sum, shasum, or openssl."
    exit 1
  fi
}

download_file() {
  URL="$1"
  DEST="$2"
  if [ "$DOWNLOADER" = "curl" ]; then
    curl -fsSL "$URL" -o "$DEST"
  else
    wget -qO "$DEST" "$URL"
  fi
}

# 5. Resolve version tag without GitHub API rate-limits
resolve_version() {
  GITHUB_HOST="${GITHUB_HOST:-https://github.com}"
  GITHUB_REPO="${GITHUB_REPO:-simple-relay/simple-relay}"

  if [ -n "${VERSION:-}" ] && [ "$VERSION" != "latest" ]; then
    case "$VERSION" in
      v*) TAG="$VERSION" ;;
      *) TAG="v$VERSION" ;;
    esac
  else
    log_info "Resolving latest release tag from GitHub..."
    if [ "$DOWNLOADER" = "curl" ]; then
      LATEST_URL="$(curl -sLI -o /dev/null -w '%{url_effective}' "${GITHUB_HOST}/${GITHUB_REPO}/releases/latest")"
    else
      LATEST_URL="$(wget -S --spider --max-redirect=0 "${GITHUB_HOST}/${GITHUB_REPO}/releases/latest" 2>&1 | awk '/Location:/{print $2}' | tr -d '\r' | tail -n 1)"
    fi
    TAG="${LATEST_URL##*/}"
    if [ -z "$TAG" ] || [ "$TAG" = "latest" ]; then
      log_error "Failed to resolve the latest release tag from GitHub."
      exit 1
    fi
  fi
  VERSION_NUM="${TAG#v}"
}

# 6. Verify checksum
verify_checksum() {
  ARCHIVE="$1"
  CHECKSUM_FILE="$2"

  grep "  ${ARCHIVE}\$" "$CHECKSUM_FILE" > checksum.match 2>/dev/null || \
  grep " ${ARCHIVE}\$" "$CHECKSUM_FILE" > checksum.match 2>/dev/null || true

  if [ ! -s checksum.match ]; then
    log_error "Checksum entry for '${ARCHIVE}' was not found in ${CHECKSUM_FILE}."
    exit 1
  fi

  if [ "$CHECKSUM_TOOL" = "sha256sum" ]; then
    sha256sum -c checksum.match >/dev/null 2>&1 || {
      log_error "SHA256 checksum verification failed."
      exit 1
    }
  elif [ "$CHECKSUM_TOOL" = "shasum" ]; then
    shasum -a 256 -c checksum.match >/dev/null 2>&1 || {
      log_error "SHA256 checksum verification failed."
      exit 1
    }
  elif [ "$CHECKSUM_TOOL" = "openssl" ]; then
    CALCULATED_HASH="$(openssl dgst -sha256 "$ARCHIVE" | awk '{print $NF}')"
    EXPECTED_HASH="$(awk '{print $1}' checksum.match)"
    if [ "$CALCULATED_HASH" != "$EXPECTED_HASH" ]; then
      log_error "SHA256 checksum mismatch (calculated: $CALCULATED_HASH, expected: $EXPECTED_HASH)."
      exit 1
    fi
  fi
}

# 7. Determine installation directory and privilege elevation
resolve_bindir() {
  USE_SUDO=""
  if [ -n "${BINDIR:-}" ]; then
    TARGET_DIR="$BINDIR"
    if [ ! -d "$TARGET_DIR" ]; then
      mkdir -p "$TARGET_DIR" 2>/dev/null || {
        if command -v sudo >/dev/null 2>&1; then
          sudo mkdir -p "$TARGET_DIR"
          USE_SUDO="sudo"
        else
          log_error "Target directory '$TARGET_DIR' does not exist and cannot be created."
          exit 1
        fi
      }
    elif [ ! -w "$TARGET_DIR" ]; then
      if command -v sudo >/dev/null 2>&1; then
        USE_SUDO="sudo"
      else
        log_error "Target directory '$TARGET_DIR' is not writable and sudo is not available."
        exit 1
      fi
    fi
  else
    # Default to /usr/local/bin if writable or running as root
    DEFAULT_DIR="/usr/local/bin"
    mkdir -p "$DEFAULT_DIR" 2>/dev/null || true
    if [ -w "$DEFAULT_DIR" ]; then
      TARGET_DIR="$DEFAULT_DIR"
    elif [ "$(id -u 2>/dev/null || echo 1)" = "0" ]; then
      TARGET_DIR="$DEFAULT_DIR"
    elif command -v sudo >/dev/null 2>&1 && sudo -n true 2>/dev/null; then
      TARGET_DIR="$DEFAULT_DIR"
      USE_SUDO="sudo"
    else
      # Fallback to user-local directory for unprivileged users
      USER_LOCAL_DIR="${HOME}/.local/bin"
      mkdir -p "$USER_LOCAL_DIR"
      TARGET_DIR="$USER_LOCAL_DIR"
    fi
  fi
}

# 8. Generate in-memory random suggested password for display banner
generate_suggested_password() {
  SUGGESTED_PASS=""
  if [ -r /dev/urandom ]; then
    SUGGESTED_PASS="$(LC_ALL=C tr -dc 'A-Za-z0-9' < /dev/urandom 2>/dev/null | head -c 16 || true)"
  fi
  if [ -z "$SUGGESTED_PASS" ] && command -v openssl >/dev/null 2>&1; then
    SUGGESTED_PASS="$(openssl rand -hex 8 2>/dev/null || true)"
  fi
  if [ -z "$SUGGESTED_PASS" ]; then
    SUGGESTED_PASS="<choose-a-strong-password>"
  fi
}

main() {
  setup_colors
  detect_os
  detect_arch
  check_dependencies
  resolve_version

  ARCHIVE_NAME="simple-relay_${VERSION_NUM}_${OS}_${ARCH}.tar.gz"
  BASE_URL="${GITHUB_HOST}/${GITHUB_REPO}/releases/download/${TAG}"
  ARCHIVE_URL="${BASE_URL}/${ARCHIVE_NAME}"
  CHECKSUM_URL="${BASE_URL}/checksums.txt"

  log_info "Installing Simple Relay ${COLOR_BOLD}${TAG}${COLOR_RESET} (${OS}/${ARCH})..."

  TMP_DIR="$(mktemp -d 2>/dev/null || mktemp -d -t 'simple-relay-install')"
  if [ -z "${TMP_DIR:-}" ] || [ ! -d "$TMP_DIR" ]; then
    log_error "Failed to create temporary directory."
    exit 1
  fi

  cleanup() {
    if [ -n "${TMP_DIR:-}" ] && [ -d "$TMP_DIR" ]; then
      rm -rf "$TMP_DIR"
    fi
  }
  trap cleanup EXIT INT TERM

  log_info "Downloading ${ARCHIVE_NAME}..."
  download_file "$ARCHIVE_URL" "${TMP_DIR}/${ARCHIVE_NAME}"

  log_info "Downloading checksums.txt..."
  download_file "$CHECKSUM_URL" "${TMP_DIR}/checksums.txt"

  log_info "Verifying SHA256 checksum..."
  (
    cd "$TMP_DIR"
    verify_checksum "$ARCHIVE_NAME" "checksums.txt"
  )
  log_success "Checksum verified."

  log_info "Extracting archive..."
  tar -xzf "${TMP_DIR}/${ARCHIVE_NAME}" -C "$TMP_DIR"

  if [ ! -f "${TMP_DIR}/simple-relay" ]; then
    log_error "Binary 'simple-relay' not found in extracted archive."
    exit 1
  fi

  resolve_bindir
  log_info "Installing binary to ${TARGET_DIR}/simple-relay..."

  if [ -n "$USE_SUDO" ]; then
    sudo cp "${TMP_DIR}/simple-relay" "${TARGET_DIR}/simple-relay"
    sudo chmod 755 "${TARGET_DIR}/simple-relay"
  else
    cp "${TMP_DIR}/simple-relay" "${TARGET_DIR}/simple-relay"
    chmod 755 "${TARGET_DIR}/simple-relay"
  fi

  SYMLINK_CREATED="false"
  if [ -e "${TARGET_DIR}/relay" ] && [ ! -L "${TARGET_DIR}/relay" ]; then
    log_warn "A regular file '${TARGET_DIR}/relay' already exists, skipping symlink."
  else
    if [ -n "$USE_SUDO" ]; then
      (cd "$TARGET_DIR" && sudo ln -sf simple-relay relay)
    else
      (cd "$TARGET_DIR" && ln -sf simple-relay relay)
    fi
    SYMLINK_CREATED="true"
  fi

  # Smoke test installed binary
  if ! "${TARGET_DIR}/simple-relay" --help >/dev/null 2>&1; then
    log_error "Binary self-test failed for ${TARGET_DIR}/simple-relay."
    exit 1
  fi

  generate_suggested_password

  echo ""
  printf "%b============================================================%b\n" "${COLOR_SUCCESS}" "${COLOR_RESET}"
  printf "%bSimple Relay %s installed successfully!%b\n" "${COLOR_BOLD}" "${TAG}" "${COLOR_RESET}"
  printf "Binary:  %s/simple-relay\n" "${TARGET_DIR}"
  if [ "$SYMLINK_CREATED" = "true" ]; then
    printf "Symlink: %s/relay\n" "${TARGET_DIR}"
  else
    printf "Symlink: %s/relay (skipped: regular file exists)\n" "${TARGET_DIR}"
  fi
  printf "%b============================================================%b\n" "${COLOR_SUCCESS}" "${COLOR_RESET}"
  echo ""
  printf "Quickstart commands:\n"
  printf "  %b# Run development mode (loopback only, zero config):%b\n" "${COLOR_INFO}" "${COLOR_RESET}"
  printf "  relay\n\n"
  printf "  %b# Run production mode (with suggested Admin Password):%b\n" "${COLOR_INFO}" "${COLOR_RESET}"
  printf "  RELAY_ADMIN_PASSWORD=\"%s\" relay\n\n" "${SUGGESTED_PASS}"

  # PATH warning
  case ":${PATH}:" in
    *:"${TARGET_DIR}":*) ;;
    *)
      log_warn "Target directory '${TARGET_DIR}' is not in your PATH."
      printf "Add it with: export PATH=\"%s:\$PATH\"\n\n" "${TARGET_DIR}"
      ;;
  esac
}

main "$@"

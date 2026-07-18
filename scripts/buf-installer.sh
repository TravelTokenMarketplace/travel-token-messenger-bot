#!/bin/bash

# Functions
show_help() {
  echo "This script downloads and installs the buf CLI utility."
  echo "Usage: $0 [OPTION]"
  echo ""
  echo "Options:"
  echo "  --version=VERSION    Install a specific version of buf, Ex: v1.28.1"
  echo "  --do-not-validate    Do not validate version from releases"
  echo "  --list               List available versions without installing"
  echo "  --help               Display this help message"
}

validate_dependencies() {
  for dep in curl jq; do
    command -v $dep >/dev/null 2>&1 || { echo >&2 "This script requires $dep but it's not installed. Aborting."; exit 1; }
  done
}

get_available_versions() {
  curl -s "${GITHUB_API_URL_RELEASES}" | jq -r '.[].tag_name'
}

validate_version() {
  local available_versions=$1
  local version_regex="^$VERSION$"
  if ! grep -qx "$version_regex" <<< "$available_versions"; then
    echo -e "Error: Version '$VERSION' not found. \nAvailable versions: \n$available_versions" >&2
    exit 1
  fi
}

list_versions() {
  local available_versions=$1
  echo "Available buf versions:"
  echo "$available_versions"
  exit 0
}

download_and_install() {
  local version=$1
  local temp_file
  temp_file=$(mktemp)

  # Build the download URL. When no version is given we use GitHub's
  # `releases/latest/download/<asset>` path, which is a plain 302 redirect to
  # the newest release asset (web, not the API) — so it needs no token and is
  # not subject to api.github.com rate limits (which otherwise made the
  # `latest` tag_name lookup return "null" in CI).
  local download_url
  if [[ -z "$version" ]]; then
    download_url="https://github.com/bufbuild/buf/releases/latest/download/buf-$(uname -s)-$(uname -m)"
    version="latest"
  else
    download_url="https://github.com/bufbuild/buf/releases/download/${version}/buf-$(uname -s)-$(uname -m)"
  fi
  echo "Downloading: $download_url"
  if ! curl -sSL "$download_url" -o "$temp_file"; then
    echo "Error: Failed to download buf version ${version}."
    rm -f "$temp_file"
    exit 1
  fi

  # Move the file to the bin directory
  if ! mv "$temp_file" "${BIN}/buf"; then
    echo "Error: Failed to move buf to ${BIN}."
    rm -f "$temp_file"
    exit 1
  fi

  # Set executable permissions
  if ! chmod +x "${BIN}/buf"; then
    echo "Error: Failed to set executable permissions on buf."
    exit 1
  fi

  echo "buf version ${version} installed successfully."
}

VERSION=""
LIST_ONLY=false
SHOW_HELP=false
DO_NOT_VALIDATE=false

# Bin directory for installation
BIN="/usr/local/bin"

# Github URLs
GITHUB_API_URL_RELEASES="https://api.github.com/repos/bufbuild/buf/releases"

# Parse arguments
while [[ $# -gt 0 ]]; do
  case $1 in
    --version=*)
      VERSION="${1#*=}"
      ;;
    --do-not-validate)
      DO_NOT_VALIDATE=true
      ;;
    --list)
      LIST_ONLY=true
      ;;
    --help)
      SHOW_HELP=true
      ;;
    *)
      echo "Unknown option: $1" >&2
      exit 1
      ;;
  esac
  shift
done

if [[ "$SHOW_HELP" == true ]]; then
  show_help
  exit 0
fi

validate_dependencies

if [[ "$LIST_ONLY" == true ]] || { [[ -n "$VERSION" ]] && [[ "$DO_NOT_VALIDATE" != true ]]; }; then
  AVAILABLE_VERSIONS=$(get_available_versions)
fi

if [[ "$LIST_ONLY" == true ]]; then
  # list_versions exits on its own
  list_versions "$AVAILABLE_VERSIONS"
fi

# An explicitly requested version is validated against the release list
# (unless --do-not-validate). With no version, download_and_install pulls the
# latest release via the redirect URL above — no api.github.com call at all.
if [[ -n "$VERSION" ]] && [[ "$DO_NOT_VALIDATE" != true ]]; then
  validate_version "$AVAILABLE_VERSIONS"
fi

download_and_install "$VERSION"

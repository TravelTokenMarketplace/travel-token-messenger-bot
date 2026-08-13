#!/bin/bash

set -e

CONDUIT_REPO="https://github.com/chain4travel/camino-conduit"
ASB_REPO="https://github.com/TravelTokenMarketplace/travel-token-matrix-app-service"

default_version="latest"
FALLBACK_BRANCH="dev"

CONDUIT_VERSION="$default_version"
ASB_VERSION="$default_version"
DEFAULT_FOUNDRY_VERSION="v1.7.1"
FOUNDRY_VERSION="$DEFAULT_FOUNDRY_VERSION"
# Pinned checksum for $DEFAULT_FOUNDRY_VERSION's linux_amd64 asset only. Verified
# unconditionally when FOUNDRY_VERSION equals the default - that is the case
# this pin exists to protect, so it is never skipped. If --foundry requests a
# different version, this checksum does not apply to it; see provision_anvil.
FOUNDRY_SHA256="cf7e688ed0c4c48adffca788b496076e31060b67ac5afe1e43dbb5499c20c88b"

BUILD_SCRIPT="./scripts/build.sh"

DEBUG=0
CLEAN=0

OUT_BINARY=""

while [[ $# -gt 0 ]]; do
    case $1 in
        --camino-conduit)
            CONDUIT_VERSION="$2"
            shift 2
            ;;
        --asb)
            ASB_VERSION="$2"
            shift 2
            ;;
        --foundry)
            FOUNDRY_VERSION="$2"
            shift 2
            ;;
		--clean)
			CLEAN=1
			shift
			;;
		--debug)
			DEBUG=1
			shift
			;;
		--filter)
			FILTER="$2"
			shift 2
			;;
        *)
            echo "Unknown argument: $1"
            exit 1
            ;;
    esac
done

ORIG_DIR=$(pwd)
dependency_dir="build/dependencies"
mkdir -p "$dependency_dir"

function download_and_extract() {
    local repo_name=$1
    local version=$2
    local repo_url=$3
    local dest_dir="$dependency_dir/$repo_name"

	echo "Processing dependency: $repo_name"

	OUT_BINARY=""

    # Remove existing directory to ensure fresh download
	if [ $CLEAN -eq 1 ] ; then
		echo "Removing existing $repo_name directory..."
		rm -rf "$dest_dir"
	fi

	local clean_url="${repo_url%.git}"
	local owner_repo=""
	if [[ "$clean_url" =~ github.com/([^/]+/[^/]+) ]]; then
		owner_repo="${BASH_REMATCH[1]}"
	elif [[ "$clean_url" =~ github.com:([^/]+/[^/]+) ]]; then
		owner_repo="${BASH_REMATCH[1]}"
	else
		owner_repo="chain4travel/$repo_name"
	fi

	echo "Owner/Repo: $owner_repo"

	release_version=""
	if [ "$version" = "latest" ]; then
		release_version=$(curl -s "https://api.github.com/repos/$owner_repo/releases/latest" | grep -Po '"tag_name": "\K[^"]*' || echo "")
	fi

	if [ -d "$dest_dir" ] ; then
		echo "Directory $dest_dir already exists. Skipping download and build."
	else 
		mkdir -p "$dest_dir"
		download_success=0
		if [ -n "$release_version" ] ; then
			local url="https://github.com/$owner_repo/releases/download/$release_version/${repo_name}-linux-amd64-${release_version}.tar.gz"
			echo "Downloading $repo_name version $release_version..."
			echo "URL: $url"
			if curl --output /dev/null --silent --head --fail "$url"; then
				curl -s -L "$url" -o "$dest_dir/${repo_name}.tar.gz"
				tar -xzf "$dest_dir/${repo_name}.tar.gz" -C "$dest_dir"
				rm "$dest_dir/${repo_name}.tar.gz"
				download_success=1
			else
				echo "WARN: Unable to download the release asset '$release_version' of $repo_name."
			fi
		fi

		if [ $download_success -eq 0 ] ; then
			if [ "$version" = "latest" ]; then
				branch=$FALLBACK_BRANCH
			else
				branch=$version
			fi

			echo "WARN: Unable to get or download the released version of $repo_name! Fallback to clone and build of the branch '$branch'."

			if git ls-remote --heads --tags "$repo_url" | grep -q "$branch"; then
				git clone --depth 1 --branch "$branch" "$repo_url" "$dest_dir"
			elif git ls-remote "$repo_url" | grep -q "$branch"; then
				git clone --depth 1 "$repo_url" "$dest_dir"
				cd "$dest_dir"
				git checkout "$branch"
			else
				echo "Version/tag/commit '$branch' not found for $repo_name, aborting."
				exit 1
			fi
			
			cd "${ORIG_DIR}/$dest_dir"
			if [ ! -f $BUILD_SCRIPT ] ; then
				echo "CRIT: No build script found at '$BUILD_SCRIPT' in cloned repository. Abort."
				exit 1
			fi
			$BUILD_SCRIPT
			cd "$ORIG_DIR"
		fi
	fi

	# if build from source
	OUT_BINARY=$dest_dir/build/$repo_name

	if [ -f "$OUT_BINARY" ] ; then
		return 0
	fi

	# camino conduit release is build like this:
	OUT_BINARY=$dest_dir/$repo_name

	if [ -f "$OUT_BINARY" ] ; then
		return 0
	fi

	# Some releases (e.g., camino-conduit v1.0.0) unpack into a versioned subdirectory:
	OUT_BINARY=$dest_dir/$repo_name-$release_version/$repo_name

	if [ -f "$OUT_BINARY" ] ; then
		return 0
	fi

	echo "CRIT: Could not find executable for '$repo_name'"
	exit 1
}

# Resolves a usable anvil into ANVIL_BIN_PATH. Prefers an anvil already on PATH;
# otherwise downloads the pinned Foundry release into build/dependencies/foundry.
function provision_anvil() {
	if command -v anvil >/dev/null 2>&1 ; then
		ANVIL_BIN_PATH="$(command -v anvil)"
		echo "Using anvil from PATH: $ANVIL_BIN_PATH ($("$ANVIL_BIN_PATH" --version | head -1))"
		return 0
	fi

	local dest_dir="$dependency_dir/foundry"
	ANVIL_BIN_PATH="$dest_dir/anvil"

	if [ $CLEAN -eq 1 ] ; then
		rm -rf "$dest_dir"
	fi

	if [ -x "$ANVIL_BIN_PATH" ] ; then
		echo "Directory $dest_dir already exists. Skipping download of foundry."
		return 0
	fi

	# The download path only supports linux/amd64: that is the single asset the
	# FOUNDRY_SHA256 pin covers, and it is what CI runs. Fail here with something
	# actionable rather than fetching a Linux ELF onto a Mac and failing later
	# with a confusing exec-format error. Other platforms are expected to install
	# foundry themselves; the PATH check above then picks it up.
	local os_name arch_name
	os_name="$(uname -s)"
	arch_name="$(uname -m)"
	if [ "$os_name" != "Linux" ] || [ "$arch_name" != "x86_64" ] ; then
		echo "CRIT: no anvil on PATH, and the automatic download supports only Linux/x86_64 (detected ${os_name}/${arch_name})."
		echo "CRIT: install foundry manually so 'anvil' is on your PATH, then re-run:"
		echo "CRIT:   curl -L https://foundry.paradigm.xyz | bash && foundryup"
		echo "CRIT: (macOS: 'brew install foundry' also works.)"
		exit 1
	fi

	local asset="foundry_${FOUNDRY_VERSION}_linux_amd64.tar.gz"
	local url="https://github.com/foundry-rs/foundry/releases/download/${FOUNDRY_VERSION}/${asset}"

	echo "Downloading foundry $FOUNDRY_VERSION..."
	mkdir -p "$dest_dir"
	if ! curl -sSL --fail "$url" -o "$dest_dir/$asset" ; then
		echo "CRIT: Unable to download foundry $FOUNDRY_VERSION from $url"
		exit 1
	fi

	if [ "$FOUNDRY_VERSION" = "$DEFAULT_FOUNDRY_VERSION" ] ; then
		echo "$FOUNDRY_SHA256  $dest_dir/$asset" | sha256sum -c - || {
			echo "CRIT: foundry checksum mismatch for $asset"
			exit 1
		}
	elif [ -n "$TTMB_FOUNDRY_SHA256" ] ; then
		echo "$TTMB_FOUNDRY_SHA256  $dest_dir/$asset" | sha256sum -c - || {
			echo "CRIT: foundry checksum mismatch for $asset"
			exit 1
		}
	else
		echo "WARN: no pinned checksum for foundry $FOUNDRY_VERSION (pin only covers $DEFAULT_FOUNDRY_VERSION)." >&2
		echo "WARN: skipping checksum verification for $asset. Set TTMB_FOUNDRY_SHA256 to verify this version." >&2
	fi

	tar xzf "$dest_dir/$asset" -C "$dest_dir"
	rm -f "$dest_dir/$asset"

	if [ ! -x "$ANVIL_BIN_PATH" ] ; then
		echo "CRIT: Could not find anvil executable in '$ANVIL_BIN_PATH'"
		exit 1
	fi
}

download_and_extract "camino-conduit" "$CONDUIT_VERSION" "$CONDUIT_REPO"
MATRIX_BIN_PATH="$OUT_BINARY"

download_and_extract "travel-token-matrix-app-service" "$ASB_VERSION" "$ASB_REPO"
ASB_BIN_PATH="$OUT_BINARY"

provision_anvil

echo "Checking dependency binaries..."

if [ ! -f "$MATRIX_BIN_PATH" ] ; then
	echo "CRIT: Unable to find camino-conduit executable in '$MATRIX_BIN_PATH'"
	exit 1
fi

if [ ! -f "$ASB_BIN_PATH" ] ; then
	echo "CRIT: Unable to find ASB executable in '$ASB_BIN_PATH'"
	exit 1
fi

if [ ! -x "$ANVIL_BIN_PATH" ] ; then
	echo "CRIT: Unable to find anvil executable in '$ANVIL_BIN_PATH'"
	exit 1
fi

# Resolved to an absolute path now (rather than down with the other binaries
# below) because the chain lifecycle test that follows runs `go test` against
# ./tests/e2e/blockchain, which changes cwd to that package directory - a
# relative ANVIL_BIN_PATH would no longer resolve from there.
ANVIL_BIN_PATH="$(realpath "${ANVIL_BIN_PATH}")"

echo "Verifying anvil chain lifecycle (Cancun activation, prefunding)..."

TTMB_TEST_ANVIL_BIN="$ANVIL_BIN_PATH" go test -tags=e2e -run TestStartChainIsReadyAndFundsKeys ./tests/e2e/blockchain/

echo "Building e2e tests..."

E2E_BIN_OUT=build/tests_e2e

ORIG_DIR=$(pwd)
cd tests/e2e
go test -tags=e2e -c -o ../../$E2E_BIN_OUT e2e_test.go
cd "$ORIG_DIR"

PARTNER_PLUGIN_BIN_PATH=build/pp-mock
TTMB_BIN_PATH=build/travel-token-messenger-bot

MATRIX_BIN_PATH="$(realpath "${MATRIX_BIN_PATH}")"
ASB_BIN_PATH="$(realpath "${ASB_BIN_PATH}")"
PARTNER_PLUGIN_BIN_PATH="$(realpath "${PARTNER_PLUGIN_BIN_PATH}")"
TTMB_BIN_PATH="$(realpath "${TTMB_BIN_PATH}")"

echo "Running e2e tests..."

ADD_PARAM=()
if [ $DEBUG -eq 1 ] ; then
	ADD_PARAM+=("-debug")
fi

if [ -n "$FILTER" ] ; then
	ADD_PARAM+=("-filter=$FILTER")
fi

./$E2E_BIN_OUT \
	-test.v \
	-anvil="${ANVIL_BIN_PATH}" \
	-matrix="${MATRIX_BIN_PATH}" \
	-asb="${ASB_BIN_PATH}" \
	-partner-plugin="${PARTNER_PLUGIN_BIN_PATH}" \
	-ttmb="${TTMB_BIN_PATH}" \
	"${ADD_PARAM[@]}"

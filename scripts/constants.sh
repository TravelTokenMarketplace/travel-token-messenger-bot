#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &>/dev/null && pwd)

# Check if this is a valid git repository
is_git=false
if git rev-parse --git-dir >/dev/null 2>&1; then
	is_git=true
fi

# Current branch
if [ "$is_git" = true ]; then
	current_branch_temp=$(git symbolic-ref -q --short HEAD || git describe --tags --always || echo unknown)
else
	current_branch_temp="unknown"
fi
# replace / with - to be a docker tag compatible
current_branch=${current_branch_temp////-}

# travel-token-messenger-bot git tag and sha
if [ "$is_git" = true ]; then
	git_commit=${TTM_BOT_COMMIT:-$(git rev-parse --short HEAD)}
	git_tag=${TTM_BOT_TAG:-$(git describe --tags --always --dirty || echo unknown)}
else
	git_commit=${TTM_BOT_COMMIT:-unknown}
	git_tag=${TTM_BOT_TAG:-unknown}
fi

# get protocol releases from buf.build
grpc_release=$("${SCRIPT_DIR}"/resolve_protocol_release.sh buf.build/gen/go/ttm/messenger-protocol/grpc/go)
protocolbuffers_release=$("${SCRIPT_DIR}"/resolve_protocol_release.sh buf.build/gen/go/ttm/messenger-protocol/protocolbuffers/go)

export current_branch
export git_commit
export git_tag
export grpc_release
export protocolbuffers_release
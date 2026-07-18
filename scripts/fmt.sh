#!/bin/bash

set -euo pipefail

if [ -z "${TTMBOT_PATH:-}" ]; then
	# travel-token-messenger-bot root folder
	TTMBOT_PATH=$(
		cd "$(dirname "${BASH_SOURCE[0]}")" || exit
		cd .. && pwd
	)
fi

echo "Formatting Go files in ${TTMBOT_PATH}..."
gofmt -w "${TTMBOT_PATH}"

echo "Formatting complete!"

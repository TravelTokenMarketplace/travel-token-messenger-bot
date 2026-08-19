#!/usr/bin/env bash

# Installs libolm-dev, the one system dependency CI needs that the runner image
# does not ship.
#
# Exists because `sudo apt update && sudo apt-get install -y libolm-dev` hangs.
# GitHub's Ubuntu images resolve the archive through a mirrorlist at
# /etc/apt/apt-mirrors.txt that lists azure.archive.ubuntu.com first. When that
# mirror stalls, apt does not fail over promptly - it retries each index with a
# long backoff and sits there. The v13.1.0-rc.3 release run burned 7m25s in this
# step and was cancelled before it built anything, while the public archive in
# the very same mirrorlist answered InRelease in 0.2s.
#
# So: pin the mirrorlist to the archive that was healthy, and bound every
# remaining network wait so a bad mirror costs seconds instead of the job. If
# the Azure mirror is ever wanted back, delete the pin block - the timeouts
# below are what keep the failure survivable either way.

set -euo pipefail

export DEBIAN_FRONTEND=noninteractive

# -o flags rather than a config drop-in: this script owns the whole apt
# interaction, and inline options survive `sudo` without an extra file to clean up.
APT_OPTS=(
	-o Acquire::ForceIPv4=true
	-o Acquire::Retries=2
	-o Acquire::http::Timeout=15
	-o Acquire::https::Timeout=15
)

MIRRORLIST=/etc/apt/apt-mirrors.txt

# Guarded so the script is still usable on a developer machine, where this file
# does not exist and apt is already pointed at a sane mirror.
if [[ -f "${MIRRORLIST}" ]]; then
	echo "Pinning ${MIRRORLIST} to the public Ubuntu archive"
	sudo tee "${MIRRORLIST}" > /dev/null <<-'MIRRORS'
		https://archive.ubuntu.com/ubuntu/
		http://archive.ubuntu.com/ubuntu/
	MIRRORS
fi

sudo -E apt-get "${APT_OPTS[@]}" update
sudo -E apt-get "${APT_OPTS[@]}" install -y --no-install-recommends libolm-dev

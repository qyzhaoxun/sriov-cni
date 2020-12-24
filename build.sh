#!/usr/bin/env bash
set -e
cd $(dirname "$0")

if [ "$(uname)" == "Darwin" ]; then
	export GOOS="${GOOS:-linux}"
fi

export GO="${GO:-go}"

mkdir -p "${PWD}/bin"

echo "Building plugins ${GOOS}"
GIT_COMMIT=$(git rev-list -1 HEAD)
LD_FLAGS="-X main.GitCommitId=$GIT_COMMIT"
PLUGINS="sriov fixipam"
for d in $PLUGINS; do
	if [ -d "$d" ]; then
		plugin="$(basename "$d")"
		if [ $plugin == "windows" ]
		then
			if [ "{$GOARCH}" == "amd64" ]
			then
				GOOS=windows . $d/build.sh
			fi
		else
			echo "  $plugin"
		        GOARCH=$ARCH $GO build -ldflags "${LD_FLAGS}" -o "${PWD}/bin/${plugin}" "${@}" ./$d
		fi
	fi
done

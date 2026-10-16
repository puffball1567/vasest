#!/bin/sh
set -eu

if [ "$#" -ne 2 ]; then
  echo "Usage: sh scripts/package-release.sh vX.Y.Z output-directory" >&2
  exit 2
fi

tag=$1
version=${tag#v}
if ! printf '%s\n' "$tag" | LC_ALL=C grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'; then
  echo "Release tag must have the form vX.Y.Z." >&2
  exit 2
fi

project_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
mkdir -p "$2"
output_dir=$(CDPATH= cd -- "$2" && pwd)
if [ -n "$(ls -A "$output_dir")" ]; then
  echo "Release output directory must be empty." >&2
  exit 2
fi
if ! grep -Fx "version = \"$version\"" "$project_dir/kinmokusei.toml" >/dev/null; then
  echo "Release tag does not match kinmokusei.toml." >&2
  exit 2
fi

temp_dir=$(mktemp -d)
trap 'rm -rf "$temp_dir"' EXIT HUP INT TERM
export GOCACHE="$temp_dir/go-cache"
keika_command=${KEIKA:-keika}

cd "$project_dir"
"$keika_command" deps lock
"$keika_command" check vasest.km
"$keika_command" emit-go -package main -o "$temp_dir/main.go" cmd/vasest/main.km
cp go.mod "$temp_dir/go.mod"
cd "$temp_dir"
CGO_ENABLED=0 go build -trimpath -buildvcs=false -o vasest main.go
test "$(./vasest --version)" = "vasest $version"

for platform in linux_amd64 linux_arm64 darwin_amd64 darwin_arm64; do
  archive="vasest_${version}_${platform}"
  mkdir -p "$archive/THIRD_PARTY_NOTICES"
  GOOS=${platform%_*} GOARCH=${platform#*_} CGO_ENABLED=0 \
    go build -trimpath -buildvcs=false -o "$archive/vasest" main.go
  cp "$project_dir/LICENSE" "$project_dir/README.md" "$archive/"
  cp "$(go env GOROOT)/LICENSE" "$archive/THIRD_PARTY_NOTICES/Go-LICENSE"
  tar -czf "$output_dir/$archive.tar.gz" "$archive"
  tar -tzf "$output_dir/$archive.tar.gz" >/dev/null
done

cd "$output_dir"
if command -v sha256sum >/dev/null 2>&1; then
  sha256sum ./*.tar.gz > SHA256SUMS
  sha256sum -c SHA256SUMS
else
  shasum -a 256 ./*.tar.gz > SHA256SUMS
  shasum -a 256 -c SHA256SUMS
fi

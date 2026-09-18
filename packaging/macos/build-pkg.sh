#!/bin/bash
set -euo pipefail

if [[ $# -ne 4 ]]; then
  echo "usage: $0 <version> <arm64-binary> <amd64-binary> <output.pkg>" >&2
  exit 64
fi
if [[ $(uname -s) != Darwin ]]; then
  echo "macOS packages must be built on macOS" >&2
  exit 69
fi

version=$1
arm64_binary=$2
amd64_binary=$3
output=$4
script_directory=$(cd "$(dirname "$0")" && pwd)
repository_root=$(cd "$script_directory/../.." && pwd)
work_directory=$(mktemp -d "${TMPDIR:-/tmp}/barrikade-lens-pkg.XXXXXX")
trap 'rm -rf "$work_directory"' EXIT

package_root="$work_directory/root"
install -d -m 0755 "$package_root/usr/local/bin" "$package_root/usr/local/share/barrikade-lens"
install -d -m 0700 "$package_root/Library/Application Support/Barrikade/Lens"
lipo -create "$arm64_binary" "$amd64_binary" -output "$package_root/usr/local/bin/barrikade-lens"
chmod 0755 "$package_root/usr/local/bin/barrikade-lens"

if [[ -n ${MACOS_APPLICATION_IDENTITY:-} ]]; then
  codesign --force --options runtime --timestamp --sign "$MACOS_APPLICATION_IDENTITY" "$package_root/usr/local/bin/barrikade-lens"
fi

install -m 0644 "$repository_root/LICENSE" "$package_root/usr/local/share/barrikade-lens/LICENSE"
install -m 0644 "$repository_root/NOTICE" "$package_root/usr/local/share/barrikade-lens/NOTICE"
install -m 0644 "$repository_root/THIRD_PARTY_NOTICES.md" "$package_root/usr/local/share/barrikade-lens/THIRD_PARTY_NOTICES.md"
install -m 0755 "$script_directory/full-cleanup.sh" "$package_root/usr/local/share/barrikade-lens/full-cleanup.sh"
xattr -cr "$package_root"

mkdir -p "$(dirname "$output")"
arguments=(
  --root "$package_root"
  --scripts "$script_directory/scripts"
  --identifier ai.barrikade.lens
  --version "$version"
  --install-location /
)
if [[ -n ${MACOS_INSTALLER_IDENTITY:-} ]]; then
  arguments+=(--sign "$MACOS_INSTALLER_IDENTITY")
fi
COPYFILE_DISABLE=1 pkgbuild "${arguments[@]}" "$output"

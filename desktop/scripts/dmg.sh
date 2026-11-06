#!/bin/sh
# Builds desktop/dist/claude-bot-<version>-arm64.dmg: the desktop app with
# the claude-bot binary inside, for Apple Silicon Macs.
#
# The app is not signed with a Developer ID, nor notarized: it is signed
# ad hoc (Apple Silicon runs no unsigned code), so the first open needs
# the steps in the README.
#
# Usage: scripts/dmg.sh <version> <claude-bot binary>
set -eu

version=$1
bin=$2
here=$(cd "$(dirname "$0")/.." && pwd)
out="$here/dist"
work="$out/work"
dmg="$out/claude-bot-$version-arm64.dmg"

case "$version" in
  [0-9]*) ;;
  *) echo "version must start with a digit, not $version" >&2; exit 2 ;;
esac

[ "$(uname -s)/$(uname -m)" = "Darwin/arm64" ] || { echo "build the DMG on an Apple Silicon Mac" >&2; exit 1; }
file "$bin" | grep -q "Mach-O 64-bit executable arm64" || { echo "$bin is not a darwin/arm64 binary" >&2; exit 1; }

rm -rf "$work" "$dmg"
mkdir -p "$work/icon.iconset"

# The icon, from desktop/icon.png (docs/logo.svg with the margin macOS icons have).
for s in 16 32 128 256 512; do
  sips -z $s $s "$here/icon.png" --out "$work/icon.iconset/icon_${s}x${s}.png" >/dev/null
  sips -z $((s * 2)) $((s * 2)) "$here/icon.png" --out "$work/icon.iconset/icon_${s}x${s}@2x.png" >/dev/null
done
iconutil -c icns "$work/icon.iconset" -o "$work/claude-bot.icns"

cd "$here"
npx --no-install electron-packager . claude-bot \
  --platform=darwin --arch=arm64 \
  --app-version="$version" --build-version="$version" \
  --app-bundle-id=com.hewenyu.claude-bot \
  --app-category-type=public.app-category.developer-tools \
  --icon="$work/claude-bot.icns" \
  --extra-resource="$bin" \
  --ignore='^/(dist|scripts|node_modules|icon\.png|tsconfig\.json)($|/)' \
  --out="$work" --overwrite --quiet

app="$work/claude-bot-darwin-arm64/claude-bot.app"
[ -x "$app/Contents/Resources/claude-bot" ] || { echo "the claude-bot binary is not in the app" >&2; exit 1; }

# Ad hoc, inside out: the binary, the helpers and frameworks, then the app.
codesign --force --deep --sign - "$app"
codesign --verify --deep --strict "$app"
"$app/Contents/Resources/claude-bot" version

# The window you see on opening the DMG: the app, and where to drag it.
mkdir -p "$work/dmg"
ditto "$app" "$work/dmg/claude-bot.app"
ln -s /Applications "$work/dmg/Applications"
hdiutil create -volname "claude-bot $version" -srcfolder "$work/dmg" -fs HFS+ -format UDZO -ov "$dmg" >/dev/null
hdiutil verify "$dmg" >/dev/null

rm -rf "$work"
ls -l "$dmg"

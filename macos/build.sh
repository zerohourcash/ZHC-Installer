#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
[[ "$(uname -s)/$(uname -m)" == Darwin/arm64 ]] || { echo 'Apple Silicon macOS required' >&2; exit 1; }
NODE_APP="${ZHC_NODE_APP:-$ROOT/../zerohourcash-modern-build/release-macos/ZHCASH Evolution.app}"
OUT="${ZHC_MACOS_OUTPUT:-$ROOT/dist-macos}"
[[ -x "$NODE_APP/Contents/MacOS/zerohour-qt" ]] || { echo 'Build Evolution Qt first or set ZHC_NODE_APP' >&2; exit 1; }
codesign --verify --deep --strict "$NODE_APP"
mkdir -p "$OUT"
STAGE="$(mktemp -d "$OUT/.stage.XXXXXX")"
trap 'rm -rf "$STAGE"' EXIT
APP="$STAGE/ZHC Installer.app"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
python3 - "$NODE_APP" "$APP/Contents/Resources/evolution-macos.zip" <<'PY'
import pathlib,sys,zipfile
root=pathlib.Path(sys.argv[1])
with zipfile.ZipFile(sys.argv[2],'w',zipfile.ZIP_DEFLATED,compresslevel=6) as z:
    for f in sorted(root.rglob('*')):
        if f.is_symlink(): raise SystemExit('Unexpected symlink in static node package')
        if f.is_file() and f.name != '.DS_Store': z.write(f, pathlib.Path('ZHCASH Evolution.app')/f.relative_to(root))
PY
DIGEST="$(shasum -a 256 "$APP/Contents/Resources/evolution-macos.zip" | cut -d ' ' -f1)"
(cd "$ROOT" && CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath \
  -ldflags "-s -w -X main.packagedNodeSHA256=$DIGEST" -o "$APP/Contents/MacOS/zhc-installer" .)
xcrun swiftc -swift-version 5 -O -parse-as-library -target arm64-apple-macosx14.0 \
  -module-cache-path "$STAGE/module-cache" "$ROOT"/macos/Sources/*.swift \
  -o "$APP/Contents/MacOS/ZHCInstaller"
cp "$NODE_APP/Contents/Resources/bitcoin.icns" "$APP/Contents/Resources/Installer.icns"
python3 - "$APP" <<'PY'
import pathlib,plistlib,sys
p=pathlib.Path(sys.argv[1])/'Contents/Info.plist'
p.write_bytes(plistlib.dumps(dict(CFBundleName='ZHC Installer', CFBundleDisplayName='ZHC Installer',
CFBundleIdentifier='org.zhcash.installer', CFBundleExecutable='ZHCInstaller', CFBundlePackageType='APPL',
CFBundleShortVersionString='0.3.0', CFBundleVersion='0.3.0', LSMinimumSystemVersion='14.0',
LSArchitecturePriority=['arm64'], CFBundleIconFile='Installer.icns', NSHighResolutionCapable=True,
LSMultipleInstancesProhibited=True)))
PY
codesign --force --options runtime --sign "${ZHC_SIGN_IDENTITY:--}" "$APP/Contents/MacOS/zhc-installer"
codesign --force --options runtime --sign "${ZHC_SIGN_IDENTITY:--}" "$APP"
codesign --verify --deep --strict "$APP"
# Rebuild only replaces generated deliverables in this output directory.
if [[ -d "$OUT/ZHC Installer.app" ]]; then rm -rf "$OUT/ZHC Installer.app"; fi
mv "$APP" "$OUT/ZHC Installer.app"
ditto -c -k --keepParent "$OUT/ZHC Installer.app" "$OUT/ZHC-Installer-0.3.0-macOS-arm64.zip"
hdiutil create -ov -volname 'ZHC Installer' -srcfolder "$OUT/ZHC Installer.app" \
  -format UDZO "$OUT/ZHC-Installer-0.3.0-macOS-arm64.dmg"
(cd "$OUT" && shasum -a 256 ZHC-Installer-0.3.0-macOS-arm64.{dmg,zip} > SHA256SUMS)
printf '%s\n' "$DIGEST" > "$OUT/EVOLUTION_PAYLOAD_SHA256"
echo "Built $OUT. Without ZHC_SIGN_IDENTITY this is an ad-hoc signed build, not notarized."

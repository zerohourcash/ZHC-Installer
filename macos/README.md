# ZHC Installer — macOS

Native SwiftUI installer for macOS 14+ on Apple Silicon (M1 and newer). The current package does not support Intel Macs. The node inside is Evolution 1.0.0 Qt, rebuilt from `3f84eeb6` using the existing ARM64 depends prefix.

## Install

1. Download the macOS DMG or application ZIP from the `v0.3.2-macos` release and check its SHA-256 against `SHA256SUMS`.
2. Open **ZHC Installer.app**. The default build uses a local ad-hoc signature; it is not Apple-notarized. This is disclosed on the release, rather than represented as a Developer ID build.
3. Read the data replacement checkbox. The installer preserves wallets, wallet backups, `.conf` files and `zhp2pproxy`, but replaces other blockchain/index/cache data.
4. Press **Начать установку**. The existing Go engine downloads the real pinned blockchain data (11,172,882,508 bytes) using its configured mirrors, verifies SHA-256, checks extraction space, extracts it and validates the required layout.
5. The included, hash-pinned Evolution Qt application is installed into `~/Applications/ZHCASH Evolution.app`. Startup uses LaunchServices and success requires local RPC readiness, not just a running process. Initial blockchain synchronization may continue after readiness.

Blockchain data: `~/Library/Application Support/ZHCASH`. Local installer log: `~/Library/Logs/ZHC Installer/install.log`. Desktop telemetry is disabled. Minimum initial free-space check is 40 GB; the engine also checks the actual ZIP uncompressed size plus 2 GiB before cleanup/extraction, without assuming that preserved old data will free enough space. More space may be needed for continued indexing/sync.

The blockchain data bytes are **not** bundled in the DMG; the node application is. The blockchain data is installed by the Go ZHC Installer, not by a simulated UI or an alternate download implementation.

If ZHC Wallet manages the node through `st.zeroscash.zerohourd`, desktop installation boots out that specific LaunchAgent before stopping node processes, keeping its plist. It does not unload the P2P proxy. Node shutdown gets up to 60 seconds to flush databases. Close another wallet/controller that actively restarts the node during installation. Existing LaunchAgent settings are not deleted; they may load again at login.

## Fresh Mac and standard paths

No preconfigured `ZHCASH_DATA_DIR` or `ZHCASH_NODE_DIR` is required. The desktop installer passes absolute paths for `~/Library/Application Support/ZHCASH` (blockchain data, node configuration and wallets) and `~/Applications` (Evolution app). It creates the data folder before saving missing variables into `zhcash-env`, and adds an idempotent source entry to `~/.zprofile` for terminal sessions. That environment file survives blockchain cleanup and cannot be overwritten by the blockchain data.

Finder does not read shell profiles. Both the initial launch and the installer’s **Open node** action therefore pass `-datadir`, `-server=1` and `-choosedatadir=0` explicitly. Direct Finder launches of Evolution use its built-in standard macOS data directory. No system-wide environment configuration, logout or reboot is needed.

Version 0.3.1 fixes first installation when the data folder does not yet exist. A temporary-home regression test covers absent ZHCASH variables, directory creation, persistence across cleanup and repeat runs without duplicate profile entries. The fix does not move existing wallets or run a live blockchain data installation during testing.

## Progress and interruption

Starfield, planet and orbit animation are procedural SwiftUI Canvas/TimelineView graphics and respect macOS Reduce Motion. Percentages describe the current stage, derived from bytes read/written. RPC startup and stages with no measurable denominator use an indeterminate indicator. Download speed and estimated time are calculated from measured transfers. Logs remain available in the interface.

Closing the app during installation does not silently leave the helper running. Use **Остановить** and wait for process exit. Cancelling extraction can leave an incomplete chain; reinstall successfully before opening the node. Retry starts the existing Go engine again; this is not a promise of crash-atomic blockchain data extraction. The macOS flow downloads and verifies the archive before removing old chain data. A per-user lock rejects another simultaneous desktop installation. App replacement uses staging and restores the previous application on replacement failure.

## Build

Prerequisites: Apple Silicon macOS, Xcode/Command Line Tools with Swift, Go 1.22+, Python 3 and a built static Evolution Qt app. No npm runtime or downloaded animation assets are needed.

```bash
ZHC_NODE_APP='/absolute/path/ZHCASH Evolution.app' bash macos/build.sh
```

Outputs are in `dist-macos/`: `.app`, DMG, ZIP, `SHA256SUMS`, and the embedded node ZIP hash. The script pins the node ZIP hash into the Go helper at build time. `ZHC_MACOS_OUTPUT` changes the output directory; rebuild replaces only generated package names there. Set `ZHC_SIGN_IDENTITY` to a configured Developer ID identity for distribution signing. Notarization/stapling must be completed separately before describing the package as notarized.

Read-only payload verification (does not stop nodes or change blockchain data):

```bash
'dist-macos/ZHC Installer.app/Contents/MacOS/zhc-installer' \
  --verify-macos-package --no-wait-on-exit --no-install-telemetry --progress-json
```

## Verification

```bash
go test -race ./...
ZHC_TEST_NODE_ARCHIVE="$PWD/dist-macos/ZHC Installer.app/Contents/Resources/evolution-macos.zip" \
  go test -run TestMacOSRealSignedPayload -v
xcrun swiftc -swift-version 5 -parse-as-library macos/Sources/InstallerModel.swift \
  macos/Tests/ModelChecks.swift -o /tmp/zhc-installer-model-checks
/tmp/zhc-installer-model-checks
codesign --verify --deep --strict 'dist-macos/ZHC Installer.app'
hdiutil verify dist-macos/ZHC-Installer-0.3.2-macOS-arm64.dmg
```

Verified during development: existing Go regressions and race checks; JSON byte progress; temporary app installation/replacement and checksum rejection; Swift model stage/error/completion checks; real signed node extraction and version launch; native installer window; Evolution Qt window/RPC/two blocks on an isolated regtest datadir. Full 11.2 GB live blockchain data installation over an existing mainnet datadir has not been performed for this release. Legacy encrypted-wallet compatibility and Apple notarization are not established by these checks.

Animation references: [Apple Canvas](https://developer.apple.com/documentation/swiftui/canvas) and [WWDC21 Canvas/TimelineView](https://developer.apple.com/videos/play/wwdc2021/10018/). The procedural artwork is implemented locally; no third-party shader code is bundled.

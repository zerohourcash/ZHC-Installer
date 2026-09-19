# ZHC Installer for macOS

Approved in conversation: native SwiftUI application with animated stars, a planet and orbit lines; actual snapshot progress; Evolution Qt installed as an application. macOS ARM64 only for this build. The user also requested GitHub release publication.

The existing Go installer remains the only snapshot engine and retains its SHA-256 pin and mirrors. SwiftUI launches the bundled helper explicitly after the user presses Install. Machine-readable events report stages, bytes, transfer speed, hash progress, extraction progress and RPC readiness. No simulated installation percentages. Stars are decorative and respect Reduce Motion.

The verified local Evolution 1.0.0 Qt app is bundled in a SHA-256-pinned ZIP. Install it into ~/Applications/ZHCASH Evolution.app using a staging directory and rollback when replacement fails. Blockchain files remain in ~/Library/Application Support/ZHCASH. Existing wallets/configuration are preserved by the installer. The welcome screen states that old blockchain data will be replaced. The archive must be downloaded and verified before the macOS flow cleans the old chain.

The UI displays errors, supports retry after failure and exposes the local log. Closing during installation requires stopping the helper; it must not continue invisibly. Telemetry is disabled for the desktop helper. Existing Linux/Windows paths remain available.

Acceptance: Go regression tests, progress and package-install tests, Swift compilation, real app launch, isolated UI failure probe, package signature/integrity checks. Full live snapshot installation must be reported separately from fixture tests; do not overwrite the developer's existing chain to test the UI. Developer ID/notarization require a signing identity; ad-hoc candidates must be labelled clearly in releases.

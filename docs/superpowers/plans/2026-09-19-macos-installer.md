# macOS Cosmic Installer Implementation Plan

**Goal:** Ship the approved macOS-only native installer while retaining the Go snapshot engine.

1. Add opt-in JSON progress events to stages, download, hash, extraction and RPC readiness. Test decoded events and byte accounting.
2. Add macOS bundled-node validation, staged app installation, rollback and LaunchServices startup. Reject missing or changed payload before touching chain data. Test temporary package installation and tampering.
3. Build SwiftUI welcome/install/error/success screens, procedural cosmos, real stage progress and local logs. Use ~/Applications and the standard data directory; disable telemetry.
4. Bundle the existing verified Evolution Qt app, compile ARM64 Swift/Go executables, sign locally and create DMG plus SHA256SUMS. Save repeatable build script.
5. Run Go tests/race checks, compile Swift, inspect the native window and exercise a controlled helper failure without modifying mainnet data. Document remaining live-snapshot/signing limits.
6. Commit only task files, publish source branch and an explicitly described macOS release with app ZIP, DMG and checksums. Verify uploaded assets. Do not modify unrelated release assets.

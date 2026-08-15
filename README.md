# ZHC-Installer

ZHC-Installer is a console installer for bootstrapping a ZHCASH node from a ready blockchain Snapshot.

It downloads `zhcash-node-seed.zip`, installs it into the standard ZHCASH data directory, preserves wallet files, and downloads the matching ZHCASH node release for the current OS.

## What the installer does

1. Detects the standard ZHCASH data directory.
2. Creates the data directory if it does not exist.
3. Checks whether a ZHCASH node process is running.
4. Stops running `zerohour-qt`, `zerohourd`, or `zerohour-cli` before changing blockchain data.
5. Removes old blockchain data while preserving wallet files.
6. Downloads `zhcash-node-seed.zip` into the data directory.
7. Extracts the Snapshot into the data directory.
8. Verifies that `blocks/` and `chainstate/` exist after extraction.
9. Downloads the ZHCASH Evolution node release for the current OS.

## Snapshot archive

Snapshot file:

```text
zhcash-node-seed.zip
```

The archive contains prepared blockchain data:

```text
blocks/
chainstate/
```

The Snapshot is a bootstrap seed. The node still verifies local data and syncs the latest blocks from the network.

## Default data directories

The installer uses the same default data directories as the node:

### Windows

```text
C:\Users\<User>\AppData\Roaming\ZHCASH
```

### Linux

```text
~/.zerohour
```

### macOS

```text
~/Library/Application Support/ZHCASH
```

## Wallet safety

The installer preserves:

```text
wallet.dat
wallet/
wallets/
*.bak
```

Old blockchain/index/cache files are removed before Snapshot extraction unless `--no-clean` is used.

## Snapshot sources

Default source order:

```text
yandex if configured → mega → github multipart → zeroscan
```

All mirrors use one shared partial file:

```text
zhcash-node-seed.zip.part
```

If one source fails, stalls, or has no progress for the configured idle timeout, the installer retries that source and then switches to the next mirror while preserving the already downloaded bytes.

Final Snapshot verification:

```text
SHA256: 20e9551f7bb35564d5f56b6ec0c908e3d23ba419eb1cc3ad266260c2857ebcf7
```

### Mega

```text
https://mega.nz/file/tzICFL5C#8avoKJxzjLjfgj2SbhBrqMo-FCqt-i2myM1XQZy49Gg
```

### Zeroscan direct HTTP

```text
https://zeroscan.io/installer/downloads/zhcash-node-seed.zip
```

### GitHub multipart fallback

GitHub Releases cannot store this Snapshot as one file because every release asset must be under 2 GiB. The installer supports the Snapshot split into 10 release assets stored in the data release:

```text
https://github.com/zerohourcash/ZHC-Installer/releases/tag/v0.2.2
```

```text
zhcash-node-seed.zip.part01
...
zhcash-node-seed.zip.part10
```

The installer downloads these parts into the same shared `zhcash-node-seed.zip.part`, resumes from the already downloaded byte offset, then verifies the final ZIP SHA256. New installer releases should not duplicate these Snapshot parts unless the Snapshot itself changes.

Yandex Disk is supported, but its URL is not published in this repository. Official release binaries may include an obfuscated build-time Yandex URL. If you build from source, provide a Yandex public-resource URL through:

```bash
export ZHCASH_YANDEX_SNAPSHOT_URL='https://disk.yandex.ru/d/...'
```

## Node release installation

The installer uses ZHCASH Evolution `v1.0.0`:

```text
https://github.com/zerohourcash/zerohourcash/releases/tag/v1.0.0
```

### Windows

Downloads the Windows release ZIP into a temporary directory, extracts only:

```text
zerohour-qt.exe
```

and copies it to the Desktop.

The release ZIP is not left on the Desktop.

### Linux

Downloads and extracts:

```text
zhcash-evolution-1.0.0-linux-x86_64.tar.gz
```

into the directory where the installer is run from, unless `--node-dir` is provided.

### macOS

The macOS node package is not available in `v1.0.0` yet. The installer installs the Snapshot and prints a message that the macOS node release will be added later.

## Downloads

Latest release:

```text
https://github.com/zerohourcash/ZHC-Installer/releases
```

## Help

Default install:

```bash
./zhc-installer-linux-amd64
```

Choose Snapshot source:

```bash
./zhc-installer-linux-amd64 --source auto
./zhc-installer-linux-amd64 --source mega
./zhc-installer-linux-amd64 --source yandex
./zhc-installer-linux-amd64 --source github
./zhc-installer-linux-amd64 --source zeroscan
```

Download reliability options:

```bash
./zhc-installer-linux-amd64 --idle-timeout 5m
./zhc-installer-linux-amd64 --source-retries 2
```

Start downloads from zero:

```bash
./zhc-installer-linux-amd64 --force
```

Use custom paths:

```bash
./zhc-installer-linux-amd64 --datadir /path/to/ZHCASH/data --node-dir /path/to/node/release
```

Skip parts:

```bash
./zhc-installer-linux-amd64 --skip-node
./zhc-installer-linux-amd64 --skip-snapshot
```

Do not remove old blockchain data before extraction:

```bash
./zhc-installer-linux-amd64 --no-clean
```

By default, the installer waits for Enter before closing on all platforms. Disable this for scripts:

```bash
./zhc-installer-linux-amd64 --no-wait-on-exit
```

## Build from source

```bash
go test ./...
go build -trimpath -ldflags="-s -w" -o zhc-installer-linux-amd64 .
```

Cross-build examples:

```bash
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o zhc-installer-windows-amd64.exe .
GOOS=linux   GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o zhc-installer-linux-amd64 .
GOOS=darwin  GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o zhc-installer-darwin-amd64 .
```

Obfuscation prevents casual extraction with tools like `strings`, but it is not a cryptographic secret once a binary is public.

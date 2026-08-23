# ZHC-Installer

ZHC-Installer is a console installer for bootstrapping a ZHCASH node from a ready blockchain Snapshot.

It downloads `zhcash-node-seed.zip`, installs it into the standard ZHCASH data directory, preserves wallet and `*.conf` configuration files, and downloads the matching ZHCASH node release for the current OS.

## Linux: install and start with one command

For a Linux server or headless machine, run:

```bash
curl -fsSL https://raw.githubusercontent.com/zerohourcash/ZHC-Installer/main/install.sh | sudo bash
```

This command downloads the latest official `zhc-installer-linux` release from GitHub, verifies it against the release `SHA256SUMS`, installs it as `/usr/local/bin/zhc-installer`, and starts it with `--no-wait-on-exit`.

The installer stops a running ZHCASH node and replaces old blockchain data with the verified Snapshot while preserving `wallet.dat`, `wallet/`, `wallets/`, `*.bak`, and `*.conf`. Read the [Wallet safety](#wallet-safety) section before running it on a node that contains a wallet.

On a Linux desktop, run the same installer without `sudo`:

```bash
curl -fsSL https://raw.githubusercontent.com/zerohourcash/ZHC-Installer/main/install.sh | bash
```

The desktop command installs the executable as `~/.local/bin/zhc-installer`. Both commands accept the regular installer flags after `bash -s --`. For example, to retain the downloaded Snapshot archive on a server:

```bash
curl -fsSL https://raw.githubusercontent.com/zerohourcash/ZHC-Installer/main/install.sh | sudo bash -s -- --keep-snapshot-archive
```

## What the installer does

1. Detects the standard ZHCASH data directory.
2. Creates the data directory if it does not exist.
3. Checks whether a ZHCASH node process is running.
4. Stops running `zerohour-qt` or `zerohourd` before changing blockchain data. Short-lived `zerohour-cli` commands are not treated as node processes.
5. Removes incomplete Snapshot partial files from previous runs.
6. Reuses `zhcash-node-seed.zip` if it already exists and passes size + SHA256 verification.
7. Removes old blockchain data while preserving wallet files, `*.conf`, and a verified Snapshot archive.
8. Downloads `zhcash-node-seed.zip` into the data directory only when a valid archive is not already present.
9. Checks again that the node is not running before extraction.
10. Removes extra data-directory files again while preserving wallet files, `*.conf`, and the downloaded Snapshot archive.
11. Extracts the Snapshot into the data directory.
12. Verifies that `blocks/` and `chainstate/` exist after extraction.
13. Deletes `zhcash-node-seed.zip` after successful extraction and verification, unless `--keep-snapshot-archive` is used.
14. Preserves all existing node settings, secures RPC for local proxy access, enables transaction/address indexes, and tunes bounded performance settings for detected CPU/RAM.
15. Ensures `addnode=127.0.0.1:3890` so a local `zhp2pproxy` native P2P overlay can be used without removing existing peers.
16. Downloads the ZHCASH Evolution node release for the current OS.
17. Starts the node after installation:
    - GUI systems start `zerohour-qt` if no node is already running.
    - Linux server/headless systems configure and restart `zerohourd.service`.

## Detailed installation flow

### 1. Path detection and environment variables

The installer resolves two working paths:

```text
ZHCASH_DATA_DIR
ZHCASH_NODE_DIR
```

`ZHCASH_DATA_DIR` is where blockchain data, chainstate, blocks, and wallet files are stored.

`ZHCASH_NODE_DIR` is where the node release files are installed or copied.

If these variables already exist in the user environment, the installer uses them as-is and does not overwrite them.

If one or both variables are missing, the installer creates them with the resolved default paths:

- Windows:
  - `ZHCASH_DATA_DIR=%APPDATA%\ZHCASH`
  - `ZHCASH_NODE_DIR=%USERPROFILE%\Desktop`
- Linux GUI:
  - `ZHCASH_DATA_DIR=$HOME/.zerohour`
  - `ZHCASH_NODE_DIR=<directory where installer was started>`
- Linux server/headless:
  - `ZHCASH_DATA_DIR=$HOME/.zerohour`
  - `ZHCASH_NODE_DIR=$HOME/ZHCASH`
  - for root this becomes `/root/ZHCASH`
- macOS:
  - `ZHCASH_DATA_DIR=$HOME/Library/Application Support/ZHCASH`
  - `ZHCASH_NODE_DIR=<directory where installer was started>`

On Windows, variables are persisted with `setx`. On Linux/macOS, variables are written to `~/.zhcash-env` and sourced from the user profile file.

### 2. Node process check

Before touching blockchain data, the installer checks for running ZHCASH processes:

```text
zerohour-qt
zerohourd
```

On Windows it checks:

```text
zerohour-qt.exe
zerohourd.exe
```

If `zerohourd.service` is active on Linux, the installer stops the systemd unit first so that `Restart=always` cannot recreate blockchain files during cleanup. It then stops any remaining node processes and waits until they exit. `zerohour-cli` is deliberately ignored because it is an RPC client rather than a node process and may be launched repeatedly by monitoring. This prevents both LevelDB/chainstate corruption and false installer stalls.

### 3. Startup cleanup

At startup, the installer removes incomplete files from previous failed runs:

```text
zhcash-node-seed.zip.part
```

If `zhcash-node-seed.zip` already exists, the installer does not blindly trust it. It checks:

1. exact expected file size;
2. full SHA256 hash.

If the ZIP is valid, it is reused and no new Snapshot download is performed.

If the ZIP is missing, incomplete, too large, or has a wrong SHA256, it is deleted and downloaded again from zero.

### 4. Blockchain data cleanup

Before extracting the Snapshot, the installer removes old blockchain/index/cache data from `ZHCASH_DATA_DIR`.

It preserves wallet-related files:

```text
wallet.dat
wallet/
wallets/
*.bak
*.conf
```

It also preserves the active verified/downloaded Snapshot archive:

```text
zhcash-node-seed.zip
```

This cleanup is performed before download/extraction and again right before extraction. The second check protects against files created while the download was running.

Before cleanup, every regular top-level `*.conf` file is also copied into memory. After Snapshot extraction, the installer verifies the contents and restores a missing or changed configuration file. It prints a warning if no `*.conf` existed before cleanup, because a previously deleted configuration cannot be recovered automatically.

After each cleanup, the installer prints every file and directory that remains. Only wallets, wallet backups, `*.conf` files, and the active Snapshot archive may remain. If any other top-level entry survives cleanup, the installer stops before extraction and reports its path.

Use `--no-clean` only when you explicitly want to keep existing non-wallet data.

### 5. Snapshot download

If a valid local `zhcash-node-seed.zip` is not available, the installer downloads the Snapshot from mirrors in this order:

```text
Yandex → Mega → GitHub multipart → Zeroscan
```

The download uses one shared partial file:

```text
zhcash-node-seed.zip.part
```

If a mirror stalls or fails, the installer retries it, then switches to the next mirror.

If `.part` is larger than the expected Snapshot size, it is treated as corrupted, deleted, and the download starts again from zero.

GitHub fallback uses 10 parts from release `v0.2.2` and assembles them into the same final ZIP.

### 6. Snapshot verification and extraction

After download, the installer verifies the full Snapshot SHA256:

```text
20e9551f7bb35564d5f56b6ec0c908e3d23ba419eb1cc3ad266260c2857ebcf7
```

Then it extracts the archive into `ZHCASH_DATA_DIR` and checks that the required directories exist:

```text
blocks/
chainstate/
```

If extraction or verification fails, the installer stops with an error.

### 7. Snapshot ZIP removal or retention

By default, after successful extraction and verification, the installer deletes:

```text
zhcash-node-seed.zip
```

This saves disk space after installation.

If you want to keep the ZIP for reuse or testing, run:

```bash
./zhc-installer-linux --keep-snapshot-archive
```

### 8. Node configuration and proxy integration

The installer updates `ZHCASH_DATA_DIR/zerohour.conf` after the Snapshot is installed. It preserves every unrelated line, existing RPC credentials, staking settings, custom peers, and other operator choices. Before changing an existing file it creates a timestamped owner-only `*.bak` copy. The active config is also restricted to owner read/write permissions.

Mandatory mainnet and local-proxy settings are added or corrected:

```text
server=1
daemon=0
rpcport=3889
rpcbind=127.0.0.1
rpcallowip=127.0.0.1
port=38100
listen=1
txindex=1
addrindex=1
prune=0
reindex=0
reindex-chainstate=0
rescan=0
deleteblockchaindata=0
zapwallettxes=0
salvagewallet=0
upgradewallet=0
checkblocks=6
checklevel=3
blocksonly=0
maxorphantx=100
addnode=127.0.0.1:3890
```

`addrindex=1` is the Evolution option required by `getaddressutxos`, `getaddressbalance`, and address-history RPC methods. `txindex=1` enables arbitrary confirmed transaction lookup. `prune=0` is enforced because pruning is incompatible with the full transaction index.

The installer operates in Snapshot-only mode during normal startup. It forces `reindex=0`, `reindex-chainstate=0`, `rescan=0`, and `deleteblockchaindata=0`, and disables destructive wallet-startup actions such as `zapwallettxes`, `salvagewallet`, and automatic wallet upgrade. Startup validation is bounded to the Evolution defaults `checkblocks=6` and `checklevel=3`; an old `checkblocks=0` cannot accidentally validate the entire chain at every start.

RPC remains loopback-only. Existing `rpcuser` and `rpcpassword` are preserved. If either is missing, the installer uses `zhcinstaller` as the local RPC user and generates a random 256-bit hexadecimal password. Passwords are never printed in installer output.

The installer detects logical CPU availability (including a Linux cgroup CPU quota), total/cgroup-limited RAM, and free disk space. It calculates bounded values for:

```text
dbcache
maxmempool
maxconnections
par
rpcthreads
rpcworkqueue
```

For example, an 8-thread server with about 20 GiB RAM receives `dbcache=4096`, `maxmempool=384`, `maxconnections=96`, and `par=7`. A low-disk warning is shown below 30 GiB, but the installer never silently enables pruning.

The current Evolution P2P port is `38100`; legacy `8003`/`3888` ports are not forced. Existing `addnode` lines are retained, while `127.0.0.1:3890` connects the node to the native ZHCASH tunnel exposed by `zhp2pproxy` when available. If the proxy is absent, the normal DNS/fixed seeds continue to work and the failed local peer is retried harmlessly.

Older configurations may contain `spentindex`, `blockfilterindex`, or `limitfreerelay`. Evolution v1.0.0 does not register these options, so the installer does not add them. Existing lines remain preserved in the config and its backup. Likewise, `whitelistrelay` and `whitelistforcerelay` are boolean options, not IP-address fields; the installer does not create the old invalid `=127.0.0.1` form.

The official Snapshot must contain the required transaction and address indexes. If it does not, the installer does not silently start a long reindex; the node will report the mismatch so that the Snapshot can be corrected or an operator can explicitly perform a one-time rebuild outside the normal installer flow.

### 9. Node release installation

After Snapshot installation, the installer downloads the ZHCASH Evolution `v1.0.0` node release.

On Windows it extracts only:

```text
zerohour-qt.exe
```

and copies it to `ZHCASH_NODE_DIR`, which defaults to the Desktop.

On Linux with GUI it downloads and extracts the Linux release archive into `ZHCASH_NODE_DIR`, preserving the previous behavior.

On Linux server/headless systems it downloads and extracts the Linux release archive into:

```text
$HOME/ZHCASH
```

For root this is:

```text
/root/ZHCASH
```

The installer treats Linux as GUI mode when desktop-session variables such as `XDG_CURRENT_DESKTOP`, `DESKTOP_SESSION`, `GDMSESSION`, or `WAYLAND_DISPLAY` are present. A plain SSH/X11 `DISPLAY` value by itself does not switch the installer to GUI mode.

On macOS, the Snapshot installation works, but the macOS node package is not available in ZHCASH `v1.0.0` yet.

### 10. Node start

After the node release is installed, the installer checks whether a node is already running.

On GUI systems:

- if a node is already running, the installer does not start another instance;
- if no node is running, it finds `zerohour-qt` / `zerohour-qt.exe` inside `ZHCASH_NODE_DIR` and starts it.

On Linux server/headless systems:

- the installer finds `zerohourd` inside `ZHCASH_NODE_DIR`;
- writes `/etc/systemd/system/zerohourd.service`;
- runs `systemctl daemon-reload`;
- runs `systemctl enable zerohourd.service`;
- runs `systemctl restart zerohourd.service`;
- the service uses `Restart=always` and `RestartSec=10`.

Linux server/headless mode requires root privileges because it writes to `/etc/systemd/system`.

### 11. Exit behavior

By default, the installer waits for Enter before closing. This is useful on Windows, where a console window would otherwise close immediately.

For scripts, disable this behavior:

```bash
./zhc-installer-linux --no-wait-on-exit
```

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

The installer first checks ZHCASH-specific environment variables:

```text
ZHCASH_DATA_DIR
ZHCASH_NODE_DIR
```

If they already exist, their paths are used and are not overwritten.

If they do not exist, the installer creates them with standard paths:

### Windows

```text
ZHCASH_DATA_DIR=C:\Users\<User>\AppData\Roaming\ZHCASH
ZHCASH_NODE_DIR=C:\Users\<User>\Desktop
```

### Linux GUI

```text
ZHCASH_DATA_DIR=~/.zerohour
ZHCASH_NODE_DIR=<installer directory>
```

### Linux server/headless

```text
ZHCASH_DATA_DIR=~/.zerohour
ZHCASH_NODE_DIR=~/ZHCASH
```

For root:

```text
ZHCASH_DATA_DIR=/root/.zerohour
ZHCASH_NODE_DIR=/root/ZHCASH
```

Server/headless mode is used when no desktop-session variables are detected. A plain SSH/X11 `DISPLAY` value by itself is still treated as server/headless.

### macOS

```text
ZHCASH_DATA_DIR=~/Library/Application Support/ZHCASH
ZHCASH_NODE_DIR=<installer directory>
```

On Windows the variables are persisted with `setx`. On Linux they are written to `~/.zhcash-env` and sourced from `~/.profile`. On macOS they are written to `~/.zhcash-env` and sourced from `~/.zprofile`.

## Wallet safety

The installer preserves:

```text
wallet.dat
wallet/
wallets/
*.bak
*.conf
```

Old blockchain/index/cache files are removed before Snapshot extraction unless `--no-clean` is used. At startup the installer deletes incomplete Snapshot partial files from previous runs:

```text
zhcash-node-seed.zip.part
```

If `zhcash-node-seed.zip` already exists, the installer verifies its expected size and SHA256. A valid archive is reused; an invalid or incomplete archive is deleted and downloaded again.

During cleanup the installer preserves the active verified/downloaded Snapshot archive:

```text
zhcash-node-seed.zip
```

The installer lists the remaining files and directories after both cleanup passes. Snapshot extraction also refuses to overwrite preserved wallet, backup, or `*.conf` paths. As a second layer of protection, regular `*.conf` files are backed up in memory before cleanup and verified or restored after extraction.

After successful extraction and verification, `zhcash-node-seed.zip` is deleted from the data directory by default. Use `--keep-snapshot-archive` to keep the ZIP after installation.

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

On headless Ubuntu servers, the installer reads `/etc/os-release` and selects the matching console-only build:

```text
Ubuntu 20.04: zhcash-evolution-1.0.0-linux-x86_64-ubuntu20.04-console.tar.gz
Ubuntu 22.04: zhcash-evolution-1.0.0-linux-x86_64-ubuntu22.04-console.tar.gz
Ubuntu 24.04: zhcash-evolution-1.0.0-linux-x86_64-ubuntu24.04-console.tar.gz
```

Each selected archive is checked against the SHA-256 value published in the official `v1.0.0` `SHA256SUMS` asset before extraction. A checksum mismatch stops installation.

Linux desktop systems, unsupported Ubuntu versions, and other Linux distributions use the compatible full fallback archive:

```text
zhcash-evolution-1.0.0-linux-x86_64.tar.gz
```

The fallback archive and Windows archive are also checked against pinned SHA-256 values before extraction.

Linux GUI mode extracts it into the directory where the installer is run from, unless `--node-dir` is provided.

Linux server/headless mode extracts it into:

```text
~/ZHCASH
```

For root:

```text
/root/ZHCASH
```

Then it configures and restarts `zerohourd.service`.

## Linux server systemd service

On Linux server/headless systems the installer creates:

```text
/etc/systemd/system/zerohourd.service
```

The service starts `zerohourd` from `ZHCASH_NODE_DIR` and passes the selected blockchain data directory through `-datadir=<ZHCASH_DATA_DIR>`.

The service is configured with automatic restart:

```text
Restart=always
RestartSec=10
```

Useful commands:

```bash
sudo systemctl status zerohourd.service
sudo systemctl restart zerohourd.service
sudo systemctl stop zerohourd.service
sudo systemctl start zerohourd.service
sudo journalctl -u zerohourd.service -f
```

Disable autostart:

```bash
sudo systemctl disable zerohourd.service
```

Remove the service completely:

```bash
sudo systemctl stop zerohourd.service
sudo systemctl disable zerohourd.service
sudo rm /etc/systemd/system/zerohourd.service
sudo systemctl daemon-reload
```

### macOS

The macOS node package is not available in `v1.0.0` yet. The installer installs the Snapshot and prints a message that the macOS node release will be added later.

## Downloads

Latest release:

```text
https://github.com/zerohourcash/ZHC-Installer/releases
```

## Help

### Step-by-step behavior

When started without extra flags, the installer:

1. Detects or creates `ZHCASH_DATA_DIR` and `ZHCASH_NODE_DIR`.
2. Uses existing `ZHCASH_DATA_DIR`/`ZHCASH_NODE_DIR` if they are already set.
3. Stops running ZHCASH node processes before changing blockchain data.
4. Removes incomplete `zhcash-node-seed.zip.part` files from old runs.
5. Reuses an existing `zhcash-node-seed.zip` only if size and SHA256 are correct.
6. Cleans old blockchain data while preserving wallets, `*.conf`, and the active Snapshot ZIP.
7. Downloads Snapshot from mirrors in order: Yandex, Mega, GitHub multipart, Zeroscan.
8. Before extraction, checks again that the node is not running.
9. Cleans the data directory again from extra files, preserving wallets, `*.conf`, and the Snapshot ZIP.
10. Extracts Snapshot into the ZHCASH data directory.
11. Verifies that `blocks/` and `chainstate/` exist.
12. Deletes the Snapshot ZIP unless `--keep-snapshot-archive` is used.
13. Preserves existing config content, creates a timestamped backup, enables local RPC/address indexes, tunes resource-dependent limits, and adds the local P2P proxy peer.
14. Downloads and installs the ZHCASH Evolution node release.
15. Starts the node:
    - GUI systems start `zerohour-qt` if no node is already running.
    - Linux server/headless systems configure and restart `zerohourd.service`.
16. Waits for Enter before closing.

Default install:

```bash
./zhc-installer-linux
```

Choose Snapshot source:

```bash
./zhc-installer-linux --source auto
./zhc-installer-linux --source mega
./zhc-installer-linux --source yandex
./zhc-installer-linux --source github
./zhc-installer-linux --source zeroscan
```

Download reliability options:

```bash
./zhc-installer-linux --idle-timeout 5m
./zhc-installer-linux --source-retries 2
```

Start downloads from zero:

```bash
./zhc-installer-linux --force
```

Keep Snapshot ZIP after successful extraction:

```bash
./zhc-installer-linux --keep-snapshot-archive
```

Use custom paths:

```bash
./zhc-installer-linux --datadir /path/to/ZHCASH/data --node-dir /path/to/node/release
```

Skip parts:

```bash
./zhc-installer-linux --skip-node
./zhc-installer-linux --skip-snapshot
```

Do not remove old blockchain data before extraction:

```bash
./zhc-installer-linux --no-clean
```

By default, the installer waits for Enter before closing on all platforms. Disable this for scripts:

```bash
./zhc-installer-linux --no-wait-on-exit
```

## Build from source

```bash
go test ./...
go build -trimpath -ldflags="-s -w" -o zhc-installer-linux .
```

Cross-build examples:

```bash
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o zhc-installer-windows.exe .
GOOS=linux   GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o zhc-installer-linux .
GOOS=darwin  GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o zhc-installer-darwin .
```

Obfuscation prevents casual extraction with tools like `strings`, but it is not a cryptographic secret once a binary is public.

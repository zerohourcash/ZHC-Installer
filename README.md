# ZHC-Installer

ZHC-Installer downloads a ready ZHCASH blockchain Snapshot seed and helps bootstrap a node faster than syncing from block zero.

The Snapshot archive is:

```text
zhcash-node-seed.zip
```

It contains prepared blockchain data such as:

```text
blocks/
chainstate/
```

After downloading and extracting the Snapshot into the standard ZHCASH data directory, the node only needs to finish the latest delta synchronization from the network.

## Public Snapshot sources

### Mega

```text
https://mega.nz/file/tzICFL5C#8avoKJxzjLjfgj2SbhBrqMo-FCqt-i2myM1XQZy49Gg
```

### Zeroscan direct HTTP

```text
https://zeroscan.io/installer/downloads/zhcash-node-seed.zip
```

## Seed downloader

Linux amd64 downloader:

```text
http://95.133.236.37:8080/installer/zhcash-seed-downloader-linux-amd64
```

SHA256:

```text
28e3375c0cbba8367c63cfa155907e70916faa07613f3a296b4b2e102c6db064
```

Download and run:

```bash
wget http://95.133.236.37:8080/installer/zhcash-seed-downloader-linux-amd64
chmod +x zhcash-seed-downloader-linux-amd64
./zhcash-seed-downloader-linux-amd64
```

The downloader saves the Snapshot archive next to the executable:

```text
zhcash-node-seed.zip
```

If the download is interrupted, run the same command again. The downloader resumes from:

```text
zhcash-node-seed.zip.part
```

## Help

Default mode:

```bash
./zhcash-seed-downloader-linux-amd64
```

Default source order:

```text
mega → yandex if configured → zeroscan
```

Select a source manually:

```bash
./zhcash-seed-downloader-linux-amd64 --source mega
./zhcash-seed-downloader-linux-amd64 --source yandex
./zhcash-seed-downloader-linux-amd64 --source zeroscan
./zhcash-seed-downloader-linux-amd64 --source auto
```

Yandex Disk is supported, but its URL is not published in this repository. Official release binaries may include an obfuscated build-time Yandex URL. If you build from source, provide a Yandex public-resource URL through an environment variable:

```bash
export ZHCASH_YANDEX_SNAPSHOT_URL='https://disk.yandex.ru/d/...'
./zhcash-seed-downloader-linux-amd64 --source yandex
```

Start download from zero:

```bash
./zhcash-seed-downloader-linux-amd64 --force
```

## Snapshot installation paths

Default ZHCASH data directory:

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

## Manual Snapshot installation

1. Stop the ZHCASH node.
2. Back up `wallet.dat`, `wallet/`, or `wallets/`.
3. Download `zhcash-node-seed.zip`.
4. Extract the archive into the ZHCASH data directory.
5. Start the node.
6. Let the node verify the local data and sync the latest blocks.

Do not delete wallet files when replacing blockchain data.

## Notes

- Mega is the default first source.
- Yandex Disk is used as fallback in `auto` mode when the release binary has a build-time Yandex URL or when `ZHCASH_YANDEX_SNAPSHOT_URL` is set.
- Zeroscan direct HTTP is used as the final fallback in `auto` mode.
- Zeroscan direct HTTP supports `Content-Length` and `Accept-Ranges`, so interrupted downloads can be resumed.
- The Snapshot is a bootstrap seed. It does not replace normal node verification.
- Obfuscation prevents casual extraction with tools like `strings`, but it is not a cryptographic secret once the binary is public.

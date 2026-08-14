package main

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const defaultMegaLink = "https://mega.nz/file/tzICFL5C#8avoKJxzjLjfgj2SbhBrqMo-FCqt-i2myM1XQZy49Gg"
const defaultZeroscanURL = "https://zeroscan.io/installer/downloads/zhcash-node-seed.zip"
const defaultOutputName = "zhcash-node-seed.zip"
const yandexURLVariable = "ZHCASH_YANDEX_SNAPSHOT_URL"

var yandexURLPayload string
var yandexURLKey string

type sourceKind string

const (
	sourceMega     sourceKind = "mega"
	sourceYandex   sourceKind = "yandex"
	sourceZeroscan sourceKind = "zeroscan"
)

type sourceConfig struct {
	Name string
	Kind sourceKind
	URL  string
}

type megaCryptoParams struct {
	Key []byte
	IV  []byte
}

type megaFileInfo struct {
	Size        int64
	DownloadURL string
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "\nERROR: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	sourceFlag := flag.String("source", "auto", "download source: auto, mega, yandex, or zeroscan")
	force := flag.Bool("force", false, "delete existing output/partial file and start over")
	flag.Parse()

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	baseDir := filepath.Dir(exe)
	outputPath := filepath.Join(baseDir, defaultOutputName)
	partPath := outputPath + ".part"

	sources, err := resolveSources(*sourceFlag)
	if err != nil {
		return err
	}

	fmt.Println("ZHCASH seed downloader")
	fmt.Println("Directory:", baseDir)
	fmt.Println("Output:", outputPath)
	fmt.Println("Source mode:", *sourceFlag)

	ctx, cancel := context.WithTimeout(context.Background(), 24*time.Hour)
	defer cancel()

	if *force {
		_ = os.Remove(outputPath)
		_ = os.Remove(partPath)
	}

	var failures []string
	for _, source := range sources {
		fmt.Println()
		fmt.Println("==> Source:", source.Name)
		if err := downloadFromSource(ctx, source, outputPath, partPath); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", source.Name, err))
			fmt.Println("Source failed:", err)
			continue
		}
		return nil
	}
	return fmt.Errorf("all sources failed: %s", strings.Join(failures, "; "))
}

func resolveSources(mode string) ([]sourceConfig, error) {
	yandex := sourceConfig{Name: "yandex", Kind: sourceYandex, URL: configuredYandexURL()}
	all := []sourceConfig{
		{Name: "mega", Kind: sourceMega, URL: defaultMegaLink},
		yandex,
		{Name: "zeroscan", Kind: sourceZeroscan, URL: defaultZeroscanURL},
	}
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "auto":
		return all, nil
	case "mega":
		return all[0:1], nil
	case "yandex", "ya", "яндекс":
		return all[1:2], nil
	case "zeroscan", "zeroscan.io":
		return all[2:3], nil
	default:
		return nil, fmt.Errorf("unknown source %q; use auto, mega, yandex, or zeroscan", mode)
	}
}

func configuredYandexURL() string {
	if decoded, err := decodeObfuscatedYandexURL(yandexURLPayload, yandexURLKey); err == nil && decoded != "" {
		return decoded
	}
	return os.Getenv(yandexURLVariable)
}

func decodeObfuscatedYandexURL(payload string, key string) (string, error) {
	if payload == "" || key == "" {
		return "", nil
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", err
	}
	keyBytes, err := base64.StdEncoding.DecodeString(key)
	if err != nil {
		return "", err
	}
	if len(keyBytes) == 0 {
		return "", errors.New("empty obfuscation key")
	}
	for i := range data {
		data[i] ^= keyBytes[i%len(keyBytes)]
	}
	return string(data), nil
}

func downloadFromSource(ctx context.Context, source sourceConfig, outputPath string, partPath string) error {
	switch source.Kind {
	case sourceMega:
		return downloadMega(ctx, source.URL, outputPath, partPath)
	case sourceYandex:
		if source.URL == "" {
			return fmt.Errorf("%s is not set", yandexURLVariable)
		}
		info, err := requestYandexFileInfo(ctx, source.URL)
		if err != nil {
			return err
		}
		return downloadPlainHTTP(ctx, info.DownloadURL, outputPath, partPath, info.Size)
	case sourceZeroscan:
		size, err := requestHTTPContentLength(ctx, source.URL)
		if err != nil {
			return err
		}
		return downloadPlainHTTP(ctx, source.URL, outputPath, partPath, size)
	default:
		return fmt.Errorf("unsupported source kind: %s", source.Kind)
	}
}

func downloadMega(ctx context.Context, megaLink string, outputPath string, partPath string) error {
	fileID, fileKey, err := parseMegaLink(megaLink)
	if err != nil {
		return err
	}
	params, err := deriveMegaCryptoParams(fileKey)
	if err != nil {
		return err
	}
	fmt.Println("Mega file id:", fileID)
	info, err := requestMegaFileInfo(ctx, fileID)
	if err != nil {
		return err
	}
	fmt.Println("Size:", humanBytes(info.Size))
	if complete, err := existingComplete(outputPath, info.Size); err != nil {
		return err
	} else if complete {
		fmt.Println("File already exists and has expected size.")
		return verifyZipHeader(outputPath)
	}
	offset, err := resumeOffset(partPath, info.Size)
	if err != nil {
		return err
	}
	if err := downloadAndDecrypt(ctx, info.DownloadURL, params, partPath, offset, info.Size); err != nil {
		return err
	}
	return finalizeDownloadedPart(outputPath, partPath)
}

func finalizeDownloadedPart(outputPath string, partPath string) error {
	if err := verifyZipHeader(partPath); err != nil {
		return err
	}
	if err := os.Rename(partPath, outputPath); err != nil {
		return err
	}
	fmt.Println("Done:", outputPath)
	return nil
}

func parseMegaLink(link string) (string, string, error) {
	u, err := url.Parse(link)
	if err != nil {
		return "", "", err
	}
	if u.Fragment == "" {
		return "", "", errors.New("Mega URL must contain decryption key after #")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == "file" && parts[i+1] != "" {
			return parts[i+1], u.Fragment, nil
		}
	}
	return "", "", fmt.Errorf("unsupported Mega file URL path: %s", u.Path)
}

func deriveMegaCryptoParams(fileKey string) (megaCryptoParams, error) {
	raw, err := base64.RawURLEncoding.DecodeString(fileKey)
	if err != nil {
		return megaCryptoParams{}, err
	}
	if len(raw) != 32 {
		return megaCryptoParams{}, fmt.Errorf("unsupported Mega key length: %d bytes", len(raw))
	}
	words := make([]uint32, 8)
	for i := range words {
		words[i] = binary.BigEndian.Uint32(raw[i*4 : i*4+4])
	}
	key := make([]byte, 16)
	binary.BigEndian.PutUint32(key[0:4], words[0]^words[4])
	binary.BigEndian.PutUint32(key[4:8], words[1]^words[5])
	binary.BigEndian.PutUint32(key[8:12], words[2]^words[6])
	binary.BigEndian.PutUint32(key[12:16], words[3]^words[7])

	iv := make([]byte, 16)
	binary.BigEndian.PutUint32(iv[0:4], words[4])
	binary.BigEndian.PutUint32(iv[4:8], words[5])
	return megaCryptoParams{Key: key, IV: iv}, nil
}

func requestMegaFileInfo(ctx context.Context, fileID string) (megaFileInfo, error) {
	payload := []map[string]any{{"a": "g", "g": 1, "p": fileID}}
	body, err := json.Marshal(payload)
	if err != nil {
		return megaFileInfo{}, err
	}
	endpoint := fmt.Sprintf("https://g.api.mega.co.nz/cs?id=%d", time.Now().UnixMilli())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return megaFileInfo{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return megaFileInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return megaFileInfo{}, fmt.Errorf("Mega API HTTP status: %s", resp.Status)
	}

	var decoded []json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return megaFileInfo{}, err
	}
	if len(decoded) == 0 {
		return megaFileInfo{}, errors.New("empty Mega API response")
	}
	var code int
	if err := json.Unmarshal(decoded[0], &code); err == nil && code < 0 {
		return megaFileInfo{}, fmt.Errorf("Mega API error code: %d", code)
	}
	var item struct {
		Size int64  `json:"s"`
		URL  string `json:"g"`
	}
	if err := json.Unmarshal(decoded[0], &item); err != nil {
		return megaFileInfo{}, err
	}
	if item.Size <= 0 || item.URL == "" {
		return megaFileInfo{}, fmt.Errorf("unexpected Mega API response: %s", string(decoded[0]))
	}
	return megaFileInfo{Size: item.Size, DownloadURL: item.URL}, nil
}

func requestYandexFileInfo(ctx context.Context, publicURL string) (megaFileInfo, error) {
	endpoint := "https://cloud-api.yandex.net/v1/disk/public/resources/download?public_key=" + url.QueryEscape(publicURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return megaFileInfo{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return megaFileInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return megaFileInfo{}, fmt.Errorf("Yandex API HTTP status: %s", resp.Status)
	}
	var item struct {
		URL string `json:"href"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
		return megaFileInfo{}, err
	}
	if item.URL == "" {
		return megaFileInfo{}, errors.New("Yandex API response has no href")
	}
	size, err := requestHTTPContentLength(ctx, item.URL)
	if err != nil {
		return megaFileInfo{}, err
	}
	return megaFileInfo{Size: size, DownloadURL: item.URL}, nil
}

func requestHTTPContentLength(ctx context.Context, sourceURL string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, sourceURL, nil)
	if err != nil {
		return 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return 0, fmt.Errorf("HTTP HEAD status: %s", resp.Status)
	}
	if resp.ContentLength <= 0 {
		return 0, errors.New("HTTP source did not provide Content-Length")
	}
	return resp.ContentLength, nil
}

func existingComplete(path string, size int64) (bool, error) {
	stat, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if stat.Size() > size {
		return false, fmt.Errorf("existing file is larger than expected: %s > %s; use --force", humanBytes(stat.Size()), humanBytes(size))
	}
	return stat.Size() == size, nil
}

func resumeOffset(partPath string, targetSize int64) (int64, error) {
	offset := int64(0)
	if stat, err := os.Stat(partPath); err == nil {
		offset = stat.Size()
		if offset > targetSize {
			return 0, fmt.Errorf("partial file is larger than expected: %s > %s; use --force", humanBytes(offset), humanBytes(targetSize))
		}
	}
	if offset > 0 {
		fmt.Printf("Resuming from %s\n", humanBytes(offset))
	}
	return offset, nil
}

func downloadPlainHTTP(ctx context.Context, sourceURL string, outputPath string, partPath string, size int64) error {
	fmt.Println("Size:", humanBytes(size))
	if complete, err := existingComplete(outputPath, size); err != nil {
		return err
	} else if complete {
		fmt.Println("File already exists and has expected size.")
		return verifyZipHeader(outputPath)
	}
	offset, err := resumeOffset(partPath, size)
	if err != nil {
		return err
	}

	out, err := os.OpenFile(partPath, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := out.Seek(offset, io.SeekStart); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, size-1))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("download HTTP status: %s", resp.Status)
	}

	progress := &progressReader{
		reader: resp.Body,
		start:  time.Now(),
		last:   time.Now(),
		offset: offset,
		total:  size,
	}
	if _, err := io.Copy(out, progress); err != nil {
		return err
	}
	fmt.Println()

	stat, err := os.Stat(partPath)
	if err != nil {
		return err
	}
	if stat.Size() != size {
		return fmt.Errorf("downloaded size mismatch: got %s, expected %s; re-run to resume", humanBytes(stat.Size()), humanBytes(size))
	}
	return finalizeDownloadedPart(outputPath, partPath)
}

func downloadAndDecrypt(ctx context.Context, sourceURL string, params megaCryptoParams, outputPath string, offset int64, targetSize int64) error {
	out, err := os.OpenFile(outputPath, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := out.Seek(offset, io.SeekStart); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return err
	}
	if offset > 0 || targetSize > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, targetSize-1))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("download HTTP status: %s", resp.Status)
	}

	block, err := aes.NewCipher(params.Key)
	if err != nil {
		return err
	}
	stream := newCTRAtOffset(block, params.IV, offset)
	reader := &cipher.StreamReader{S: stream, R: resp.Body}
	progress := &progressReader{
		reader: reader,
		start:  time.Now(),
		last:   time.Now(),
		offset: offset,
		total:  targetSize,
	}

	if _, err := io.Copy(out, progress); err != nil {
		return err
	}
	fmt.Println()

	stat, err := os.Stat(outputPath)
	if err != nil {
		return err
	}
	if stat.Size() != targetSize {
		return fmt.Errorf("downloaded size mismatch: got %s, expected %s; re-run to resume", humanBytes(stat.Size()), humanBytes(targetSize))
	}
	return nil
}

func newCTRAtOffset(block cipher.Block, iv []byte, offset int64) cipher.Stream {
	ctrIV := append([]byte(nil), iv...)
	blocks := uint64(offset / int64(block.BlockSize()))
	for i := len(ctrIV) - 1; blocks > 0 && i >= 0; i-- {
		sum := uint64(ctrIV[i]) + (blocks & 0xff)
		ctrIV[i] = byte(sum)
		blocks = (blocks >> 8) + (sum >> 8)
	}
	stream := cipher.NewCTR(block, ctrIV)
	if skip := int(offset % int64(block.BlockSize())); skip > 0 {
		dummy := make([]byte, skip)
		stream.XORKeyStream(dummy, dummy)
	}
	return stream
}

func verifyZipHeader(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	head := make([]byte, 4)
	if _, err := io.ReadFull(file, head); err != nil {
		return err
	}
	if !bytes.Equal(head, []byte{'P', 'K', 0x03, 0x04}) {
		return fmt.Errorf("not a ZIP file: header %x", head)
	}
	fmt.Println("ZIP header OK")
	return nil
}

type progressReader struct {
	reader io.Reader
	start  time.Time
	last   time.Time
	offset int64
	total  int64
	read   int64
}

func (p *progressReader) Read(buf []byte) (int, error) {
	n, err := p.reader.Read(buf)
	if n > 0 {
		p.read += int64(n)
		now := time.Now()
		if now.Sub(p.last) >= time.Second {
			p.print(now)
			p.last = now
		}
	}
	if errors.Is(err, io.EOF) {
		p.print(time.Now())
	}
	return n, err
}

func (p *progressReader) print(now time.Time) {
	done := p.offset + p.read
	elapsed := now.Sub(p.start).Seconds()
	speed := float64(p.read)
	if elapsed > 0 {
		speed /= elapsed
	}
	percent := int64(0)
	if p.total > 0 {
		percent = done * 100 / p.total
	}
	eta := "--"
	if speed > 0 && p.total > done {
		eta = (time.Duration(float64(p.total-done)/speed) * time.Second).Round(time.Second).String()
	}
	fmt.Printf("\rDownloaded: %s / %s | %d%% | %s/s | ETA %s",
		humanBytes(done),
		humanBytes(p.total),
		percent,
		humanBytes(int64(speed)),
		eta,
	)
}

func humanBytes(value int64) string {
	const unit = 1024
	if value < unit {
		return fmt.Sprintf("%d B", value)
	}
	div, exp := int64(unit), 0
	for n := value / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(value)/float64(div), "KMGTPE"[exp])
}

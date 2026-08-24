package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type systemResources struct {
	CPUCount      int
	MemoryBytes   uint64
	DiskFreeBytes uint64
}

type nodeConfigSetting struct {
	Key    string
	Value  string
	Secret bool
}

type nodeConfigUpdate struct {
	Created             bool
	BackupPath          string
	ChangedKeys         []string
	AddressIndexChanged bool
	TxIndexChanged      bool
}

func configureOptimizedNode(dataDir string) (nodeConfigUpdate, error) {
	resources := detectSystemResources(dataDir)
	settings := optimizedNodeSettings(resources)
	configPath := filepath.Join(dataDir, "zerohour.conf")
	original, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return nodeConfigUpdate{}, err
	}
	settings, err = addRPCAuthenticationSettings(settings, string(original))
	if err != nil {
		return nodeConfigUpdate{}, err
	}
	printSystemResources(resources, settings)
	result, err := updateNodeConfig(configPath, settings, []nodeConfigSetting{{Key: "addnode", Value: "127.0.0.1:3890"}}, time.Now().UTC())
	if err != nil {
		return nodeConfigUpdate{}, err
	}
	if result.Created {
		fmt.Println("Created node configuration:", configPath)
	} else if len(result.ChangedKeys) == 0 {
		fmt.Println("Node configuration already optimal:", configPath)
	} else {
		fmt.Println("Updated node configuration:", configPath)
	}
	if result.BackupPath != "" {
		fmt.Println("Original configuration backup:", result.BackupPath)
	}
	if len(result.ChangedKeys) > 0 {
		fmt.Println("Managed settings updated:", strings.Join(result.ChangedKeys, ", "))
	}
	if result.AddressIndexChanged || result.TxIndexChanged {
		fmt.Println("Index settings changed. Snapshot-only safety keeps rescan/reindex disabled; the official Snapshot must already contain the required indexes.")
	}
	return result, nil
}

func detectSystemResources(dataDir string) systemResources {
	resources := systemResources{CPUCount: effectiveCPUCount(runtime.NumCPU())}
	switch runtime.GOOS {
	case "linux":
		resources.MemoryBytes = linuxMemoryBytes()
	case "darwin":
		resources.MemoryBytes = commandUint64("sysctl", "-n", "hw.memsize")
	case "windows":
		kilobytes := commandUint64("powershell", "-NoProfile", "-NonInteractive", "-Command", "(Get-CimInstance Win32_OperatingSystem).TotalVisibleMemorySize")
		resources.MemoryBytes = kilobytes * 1024
	}
	resources.DiskFreeBytes = diskFreeBytes(dataDir, runtime.GOOS)
	return resources
}

func effectiveCPUCount(detected int) int {
	if detected < 1 {
		detected = 1
	}
	data, err := os.ReadFile("/sys/fs/cgroup/cpu.max")
	if err != nil {
		return detected
	}
	fields := strings.Fields(string(data))
	if len(fields) != 2 || fields[0] == "max" {
		return detected
	}
	quota, quotaErr := strconv.ParseUint(fields[0], 10, 64)
	period, periodErr := strconv.ParseUint(fields[1], 10, 64)
	if quotaErr != nil || periodErr != nil || quota == 0 || period == 0 {
		return detected
	}
	limited := int((quota + period - 1) / period)
	if limited < 1 {
		limited = 1
	}
	if limited < detected {
		return limited
	}
	return detected
}

func linuxMemoryBytes() uint64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	memory := parseLinuxMemTotal(string(data))
	for _, candidate := range []string{"/sys/fs/cgroup/memory.max", "/sys/fs/cgroup/memory/memory.limit_in_bytes"} {
		limitData, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		value := strings.TrimSpace(string(limitData))
		if value == "" || value == "max" {
			continue
		}
		limit, err := strconv.ParseUint(value, 10, 64)
		if err == nil && limit > 0 && limit < (uint64(1)<<60) && (memory == 0 || limit < memory) {
			memory = limit
		}
	}
	return memory
}

func parseLinuxMemTotal(content string) uint64 {
	for _, line := range strings.Split(content, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "MemTotal:" {
			kilobytes, err := strconv.ParseUint(fields[1], 10, 64)
			if err == nil {
				return kilobytes * 1024
			}
		}
	}
	return 0
}

func commandUint64(name string, args ...string) uint64 {
	output, err := exec.Command(name, args...).Output()
	if err != nil {
		return 0
	}
	value, err := strconv.ParseUint(strings.TrimSpace(string(output)), 10, 64)
	if err != nil {
		return 0
	}
	return value
}

func diskFreeBytes(dataDir string, goos string) uint64 {
	if goos == "windows" {
		script := fmt.Sprintf("(Get-Item -LiteralPath '%s').PSDrive.Free", strings.ReplaceAll(dataDir, "'", "''"))
		return commandUint64("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	}
	output, err := exec.Command("df", "-Pk", dataDir).Output()
	if err != nil {
		return 0
	}
	return parseDFAvailableBytes(string(output))
}

func parseDFAvailableBytes(content string) uint64 {
	lines := strings.Split(strings.TrimSpace(content), "\n")
	if len(lines) < 2 {
		return 0
	}
	fields := strings.Fields(lines[len(lines)-1])
	if len(fields) < 4 {
		return 0
	}
	kilobytes, err := strconv.ParseUint(fields[3], 10, 64)
	if err != nil {
		return 0
	}
	return kilobytes * 1024
}

func optimizedNodeSettings(resources systemResources) []nodeConfigSetting {
	cpu := resources.CPUCount
	if cpu < 1 {
		cpu = 1
	}
	memoryMiB := resources.MemoryBytes / (1024 * 1024)
	if memoryMiB == 0 {
		memoryMiB = 2048
	}
	dbCache := 256
	maxMempool := 64
	switch {
	case memoryMiB >= 32768:
		dbCache = minInt(8192, int(memoryMiB/6))
		maxMempool = 512
	case memoryMiB >= 16384:
		dbCache = 4096
		maxMempool = 384
	case memoryMiB >= 8192:
		dbCache = 2048
		maxMempool = 256
	case memoryMiB >= 4096:
		dbCache = 1024
		maxMempool = 128
	case memoryMiB >= 2048:
		dbCache = 512
		maxMempool = 96
	}
	rpcThreads := clampInt(cpu, 4, 16)
	rpcQueue := clampInt(rpcThreads*16, 64, 256)
	maxConnections := clampInt(32+cpu*8, 48, 160)
	if memoryMiB < 2048 {
		maxConnections = minInt(maxConnections, 48)
	} else if memoryMiB < 4096 {
		maxConnections = minInt(maxConnections, 64)
	}
	verificationThreads := 1
	if cpu > 2 {
		verificationThreads = minInt(cpu-1, 16)
	}
	return []nodeConfigSetting{
		{Key: "server", Value: "1"},
		{Key: "daemon", Value: "0"},
		{Key: "debug", Value: "1"},
		{Key: "rpcport", Value: "3889"},
		{Key: "rpcbind", Value: "127.0.0.1"},
		{Key: "rpcallowip", Value: "127.0.0.1"},
		{Key: "port", Value: "38100"},
		{Key: "listen", Value: "1"},
		{Key: "txindex", Value: "1"},
		{Key: "addrindex", Value: "1"},
		{Key: "prune", Value: "0"},
		{Key: "reindex", Value: "0"},
		{Key: "reindex-chainstate", Value: "0"},
		{Key: "rescan", Value: "0"},
		{Key: "deleteblockchaindata", Value: "0"},
		{Key: "zapwallettxes", Value: "0"},
		{Key: "salvagewallet", Value: "0"},
		{Key: "upgradewallet", Value: "0"},
		{Key: "checkblocks", Value: "6"},
		{Key: "checklevel", Value: "3"},
		{Key: "blocksonly", Value: "0"},
		{Key: "maxorphantx", Value: "100"},
		{Key: "dbcache", Value: strconv.Itoa(dbCache)},
		{Key: "maxmempool", Value: strconv.Itoa(maxMempool)},
		{Key: "maxconnections", Value: strconv.Itoa(maxConnections)},
		{Key: "par", Value: strconv.Itoa(verificationThreads)},
		{Key: "rpcthreads", Value: strconv.Itoa(rpcThreads)},
		{Key: "rpcworkqueue", Value: strconv.Itoa(rpcQueue)},
	}
}

func addRPCAuthenticationSettings(settings []nodeConfigSetting, original string) ([]nodeConfigSetting, error) {
	user, _ := activeMainConfigValue(original, "rpcuser")
	password, _ := activeMainConfigValue(original, "rpcpassword")
	if strings.TrimSpace(user) == "" {
		user = "zhcinstaller"
	}
	if strings.TrimSpace(password) == "" {
		random := make([]byte, 32)
		if _, err := rand.Read(random); err != nil {
			return nil, fmt.Errorf("generate RPC password: %w", err)
		}
		password = hex.EncodeToString(random)
	}
	return append(settings,
		nodeConfigSetting{Key: "rpcuser", Value: user},
		nodeConfigSetting{Key: "rpcpassword", Value: password, Secret: true},
	), nil
}

func printSystemResources(resources systemResources, settings []nodeConfigSetting) {
	fmt.Println()
	fmt.Println("==> OPTIMIZE NODE CONFIGURATION")
	fmt.Printf("Detected CPU threads: %d\n", resources.CPUCount)
	if resources.MemoryBytes > 0 {
		fmt.Printf("Detected memory: %.1f GiB\n", float64(resources.MemoryBytes)/(1024*1024*1024))
	} else {
		fmt.Println("Detected memory: unavailable; using conservative defaults")
	}
	if resources.DiskFreeBytes > 0 {
		fmt.Printf("Free disk space: %.1f GiB\n", float64(resources.DiskFreeBytes)/(1024*1024*1024))
		if resources.DiskFreeBytes < 30*1024*1024*1024 {
			fmt.Println("WARNING: less than 30 GiB is free; full tx/address indexes may require additional disk space.")
		}
	}
	values := make([]string, 0, len(settings))
	for _, setting := range settings {
		value := setting.Value
		if setting.Secret {
			value = "<preserved-or-generated>"
		}
		values = append(values, setting.Key+"="+value)
	}
	fmt.Println("Selected managed settings:", strings.Join(values, ", "))
}

func updateNodeConfig(path string, settings []nodeConfigSetting, requiredValues []nodeConfigSetting, now time.Time) (nodeConfigUpdate, error) {
	original, err := os.ReadFile(path)
	exists := err == nil
	if err != nil && !os.IsNotExist(err) {
		return nodeConfigUpdate{}, err
	}
	if exists {
		if _, err := os.Stat(path); err != nil {
			return nodeConfigUpdate{}, err
		}
	}
	updated, changedKeys, addrChanged, txChanged := rewriteNodeConfig(string(original), settings)
	for _, required := range requiredValues {
		var added bool
		updated, added = ensureNodeConfigValue(updated, required.Key, required.Value)
		if added {
			changedKeys = append(changedKeys, required.Key)
		}
	}
	result := nodeConfigUpdate{
		Created:             !exists,
		ChangedKeys:         changedKeys,
		AddressIndexChanged: addrChanged,
		TxIndexChanged:      txChanged,
	}
	if exists && updated == string(original) {
		if err := os.Chmod(path, 0o600); err != nil {
			return nodeConfigUpdate{}, fmt.Errorf("secure node configuration permissions: %w", err)
		}
		return result, nil
	}
	if exists {
		backupPath := path + ".before-installer-" + now.Format("20060102-150405.000000000") + ".bak"
		if err := os.WriteFile(backupPath, original, 0o600); err != nil {
			return nodeConfigUpdate{}, fmt.Errorf("backup node configuration: %w", err)
		}
		result.BackupPath = backupPath
	}
	if err := os.WriteFile(path, []byte(updated), 0o600); err != nil {
		return nodeConfigUpdate{}, fmt.Errorf("write node configuration: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nodeConfigUpdate{}, fmt.Errorf("secure node configuration permissions: %w", err)
	}
	return result, nil
}

func activeMainConfigValue(content string, wantedKey string) (string, bool) {
	section := ""
	value := ""
	found := false
	for _, line := range splitLinesKeepingEndings(content) {
		body := strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"))
		if strings.HasPrefix(body, "[") && strings.HasSuffix(body, "]") {
			section = strings.ToLower(strings.TrimSpace(body[1 : len(body)-1]))
			continue
		}
		key, current, ok := activeConfigAssignment(line)
		if ok && key == strings.ToLower(wantedKey) && (section == "" || section == "main") {
			value = current
			found = true
		}
	}
	return value, found
}

func ensureNodeConfigValue(content string, key string, value string) (string, bool) {
	section := ""
	for _, line := range splitLinesKeepingEndings(content) {
		body := strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"))
		if strings.HasPrefix(body, "[") && strings.HasSuffix(body, "]") {
			section = strings.ToLower(strings.TrimSpace(body[1 : len(body)-1]))
			continue
		}
		currentKey, currentValue, ok := activeConfigAssignment(line)
		if ok && currentKey == strings.ToLower(key) && currentValue == value && (section == "" || section == "main") {
			return content, false
		}
	}
	newline := "\n"
	if strings.Contains(content, "\r\n") {
		newline = "\r\n"
	}
	return key + "=" + value + newline + content, true
}

func rewriteNodeConfig(original string, settings []nodeConfigSetting) (string, []string, bool, bool) {
	values := make(map[string]string, len(settings))
	order := make([]string, 0, len(settings))
	for _, setting := range settings {
		key := strings.ToLower(setting.Key)
		values[key] = setting.Value
		order = append(order, key)
	}
	newline := "\n"
	if strings.Contains(original, "\r\n") {
		newline = "\r\n"
	}
	lines := splitLinesKeepingEndings(original)
	seen := make(map[string]bool, len(settings))
	preScanSection := ""
	for _, line := range lines {
		body := strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"))
		if strings.HasPrefix(body, "[") && strings.HasSuffix(body, "]") {
			preScanSection = strings.ToLower(strings.TrimSpace(body[1 : len(body)-1]))
			continue
		}
		key, _, ok := activeConfigAssignment(line)
		if _, managed := values[key]; ok && managed && (preScanSection == "" || preScanSection == "main") {
			seen[key] = true
		}
	}
	changed := make(map[string]bool, len(settings))
	addressIndexChanged := false
	txIndexChanged := false
	section := ""
	insertedMissing := false
	output := make([]string, 0, len(lines)+len(settings))
	appendMissing := func() {
		if insertedMissing {
			return
		}
		for _, key := range order {
			if seen[key] {
				continue
			}
			output = append(output, key+"="+values[key]+newline)
			seen[key] = true
			changed[key] = true
			if key == "addrindex" {
				addressIndexChanged = true
			}
			if key == "txindex" {
				txIndexChanged = true
			}
		}
		insertedMissing = true
	}
	for _, line := range lines {
		body := strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"))
		if strings.HasPrefix(body, "[") && strings.HasSuffix(body, "]") {
			if section == "" {
				appendMissing()
			}
			section = strings.ToLower(strings.TrimSpace(body[1 : len(body)-1]))
			output = append(output, line)
			continue
		}
		key, currentValue, ok := activeConfigAssignment(line)
		managedSection := section == "" || section == "main"
		desired, managed := values[key]
		if !ok || !managed || !managedSection {
			output = append(output, line)
			continue
		}
		seen[key] = true
		if currentValue != desired {
			line = replaceConfigValue(line, desired)
			changed[key] = true
			if key == "addrindex" {
				addressIndexChanged = true
			}
			if key == "txindex" {
				txIndexChanged = true
			}
		}
		output = append(output, line)
	}
	appendMissing()
	changedKeys := make([]string, 0, len(changed))
	for _, key := range order {
		if changed[key] {
			changedKeys = append(changedKeys, key)
		}
	}
	return strings.Join(output, ""), changedKeys, addressIndexChanged, txIndexChanged
}

func splitLinesKeepingEndings(content string) []string {
	if content == "" {
		return nil
	}
	scanner := bufio.NewScanner(strings.NewReader(content))
	scanner.Split(scanLinesWithEndings)
	lines := make([]string, 0)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines
}

func scanLinesWithEndings(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if index := strings.IndexByte(string(data), '\n'); index >= 0 {
		return index + 1, data[:index+1], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

func activeConfigAssignment(line string) (string, string, bool) {
	body := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	trimmed := strings.TrimSpace(body)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
		return "", "", false
	}
	equals := strings.IndexByte(body, '=')
	if equals < 0 {
		return "", "", false
	}
	key := strings.ToLower(strings.TrimSpace(body[:equals]))
	key = strings.TrimPrefix(key, "-")
	if key == "" {
		return "", "", false
	}
	value := strings.TrimSpace(configValueWithoutComment(body[equals+1:]))
	return key, value, true
}

func configValueWithoutComment(value string) string {
	for index, char := range value {
		if (char == '#' || char == ';') && (index == 0 || value[index-1] == ' ' || value[index-1] == '\t') {
			return value[:index]
		}
	}
	return value
}

func replaceConfigValue(line string, desired string) string {
	newline := ""
	body := line
	if strings.HasSuffix(body, "\r\n") {
		body = strings.TrimSuffix(body, "\r\n")
		newline = "\r\n"
	} else if strings.HasSuffix(body, "\n") {
		body = strings.TrimSuffix(body, "\n")
		newline = "\n"
	}
	equals := strings.IndexByte(body, '=')
	if equals < 0 {
		return line
	}
	right := body[equals+1:]
	leadingLength := len(right) - len(strings.TrimLeft(right, " \t"))
	leading := right[:leadingLength]
	suffix := ""
	for index, char := range right {
		if (char == '#' || char == ';') && (index == 0 || right[index-1] == ' ' || right[index-1] == '\t') {
			commentStart := index
			for commentStart > 0 && (right[commentStart-1] == ' ' || right[commentStart-1] == '\t') {
				commentStart--
			}
			suffix = right[commentStart:]
			break
		}
	}
	return body[:equals+1] + leading + desired + suffix + newline
}

func clampInt(value int, minimum int, maximum int) int {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func minInt(first int, second int) int {
	if first < second {
		return first
	}
	return second
}

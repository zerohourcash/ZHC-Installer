package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRewriteNodeConfigPreservesExistingContentAndUpdatesManagedValues(t *testing.T) {
	original := strings.Join([]string{
		"# operator settings",
		"server=0 # must become enabled",
		"rpcuser=existing-user",
		"rpcpassword=existing-password",
		"spentindex=1",
		"blockfilterindex=1",
		"limitfreerelay=0.0001",
		"addnode=212.34.129.167:8003",
		"custom-option=keep-me",
		"[main]",
		"addrindex=0",
		"[test]",
		"server=0",
	}, "\n") + "\n"
	settings := []nodeConfigSetting{
		{Key: "server", Value: "1"},
		{Key: "txindex", Value: "1"},
		{Key: "addrindex", Value: "1"},
		{Key: "prune", Value: "0"},
		{Key: "dbcache", Value: "4096"},
	}

	updated, changed, addressChanged, txChanged := rewriteNodeConfig(original, settings)
	for _, preserved := range []string{
		"# operator settings\n",
		"rpcuser=existing-user\n",
		"rpcpassword=existing-password\n",
		"spentindex=1\n",
		"blockfilterindex=1\n",
		"limitfreerelay=0.0001\n",
		"addnode=212.34.129.167:8003\n",
		"custom-option=keep-me\n",
		"[test]\nserver=0\n",
	} {
		if !strings.Contains(updated, preserved) {
			t.Fatalf("existing configuration content was not preserved: %q\n%s", preserved, updated)
		}
	}
	if !strings.Contains(updated, "server=1 # must become enabled\n") {
		t.Fatalf("server was not enabled while preserving its inline note:\n%s", updated)
	}
	if !strings.Contains(updated, "[main]\naddrindex=1\n") {
		t.Fatalf("main address index was not updated:\n%s", updated)
	}
	if strings.Count(updated, "addrindex=") != 1 {
		t.Fatalf("address index was duplicated:\n%s", updated)
	}
	for _, required := range []string{"txindex=1\n", "prune=0\n", "dbcache=4096\n"} {
		if !strings.Contains(updated, required) {
			t.Fatalf("missing managed setting %q:\n%s", required, updated)
		}
	}
	if !addressChanged || !txChanged {
		t.Fatalf("index changes were not detected: addr=%v tx=%v", addressChanged, txChanged)
	}
	if strings.Join(changed, ",") != "server,txindex,addrindex,prune,dbcache" {
		t.Fatalf("unexpected changed keys: %v", changed)
	}
}

func TestUpdateNodeConfigCreatesBackupAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "zerohour.conf")
	original := "rpcuser=alice\nrpcpassword=secret\nserver=0\naddnode=seed.example:38100\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	settings := []nodeConfigSetting{
		{Key: "server", Value: "1"},
		{Key: "rpcuser", Value: "alice"},
		{Key: "rpcpassword", Value: "secret", Secret: true},
	}
	now := time.Date(2026, 8, 23, 10, 11, 12, 13, time.UTC)
	result, err := updateNodeConfig(path, settings, []nodeConfigSetting{{Key: "addnode", Value: "127.0.0.1:3890"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.BackupPath == "" {
		t.Fatal("expected timestamped configuration backup")
	}
	backup, err := os.ReadFile(result.BackupPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != original {
		t.Fatalf("backup changed: %q", backup)
	}
	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(updated)
	for _, expected := range []string{
		"rpcuser=alice\n",
		"rpcpassword=secret\n",
		"server=1\n",
		"addnode=seed.example:38100\n",
		"addnode=127.0.0.1:3890\n",
	} {
		if !strings.Contains(content, expected) {
			t.Fatalf("updated config is missing %q:\n%s", expected, content)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("configuration permissions are not private: %o", info.Mode().Perm())
	}
	second, err := updateNodeConfig(path, settings, []nodeConfigSetting{{Key: "addnode", Value: "127.0.0.1:3890"}}, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(second.ChangedKeys) != 0 || second.BackupPath != "" {
		t.Fatalf("second update should be idempotent: %#v", second)
	}
}

func TestRPCAuthenticationPreservesCredentialsAndGeneratesMissingPassword(t *testing.T) {
	settings, err := addRPCAuthenticationSettings(nil, "rpcuser=operator\nrpcpassword=existing-secret\n")
	if err != nil {
		t.Fatal(err)
	}
	if settings[0].Value != "operator" || settings[1].Value != "existing-secret" || !settings[1].Secret {
		t.Fatalf("existing RPC credentials were not preserved: %#v", settings)
	}
	generated, err := addRPCAuthenticationSettings(nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if generated[0].Value != "zhcinstaller" || len(generated[1].Value) != 64 || !generated[1].Secret {
		t.Fatalf("unexpected generated RPC credentials: user=%q password_length=%d", generated[0].Value, len(generated[1].Value))
	}
}

func TestOptimizedNodeSettingsForRepresentativeServers(t *testing.T) {
	tests := []struct {
		name        string
		resources   systemResources
		dbCache     string
		mempool     string
		connections string
		par         string
	}{
		{name: "small", resources: systemResources{CPUCount: 2, MemoryBytes: 2 * 1024 * 1024 * 1024}, dbCache: "512", mempool: "96", connections: "48", par: "1"},
		{name: "medium", resources: systemResources{CPUCount: 8, MemoryBytes: 20 * 1024 * 1024 * 1024}, dbCache: "4096", mempool: "384", connections: "96", par: "7"},
		{name: "large", resources: systemResources{CPUCount: 32, MemoryBytes: 64 * 1024 * 1024 * 1024}, dbCache: "8192", mempool: "512", connections: "160", par: "16"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values := settingValues(optimizedNodeSettings(test.resources))
			for key, expected := range map[string]string{
				"server": "1", "daemon": "0", "rpcport": "3889", "rpcbind": "127.0.0.1",
				"rpcallowip": "127.0.0.1", "port": "38100", "listen": "1", "txindex": "1",
				"addrindex": "1", "prune": "0", "dbcache": test.dbCache, "maxmempool": test.mempool,
				"reindex": "0", "reindex-chainstate": "0", "rescan": "0", "deleteblockchaindata": "0",
				"zapwallettxes": "0", "salvagewallet": "0", "upgradewallet": "0", "checkblocks": "6",
				"checklevel": "3", "maxconnections": test.connections, "par": test.par,
			} {
				if values[key] != expected {
					t.Fatalf("%s: got %q, want %q", key, values[key], expected)
				}
			}
		})
	}
}

func TestResourceOutputParsers(t *testing.T) {
	if got := parseLinuxMemTotal("MemTotal:       20476212 kB\nMemFree: 1 kB\n"); got != 20476212*1024 {
		t.Fatalf("unexpected Linux memory: %d", got)
	}
	df := "Filesystem 1024-blocks Used Available Capacity Mounted on\n/dev/vda1 100000 40000 60000 40% /\n"
	if got := parseDFAvailableBytes(df); got != 60000*1024 {
		t.Fatalf("unexpected free disk bytes: %d", got)
	}
}

func TestLegacyProxyNodeConfigIsAdaptedWithoutLosingOperatorSettings(t *testing.T) {
	original := `server=1
daemon=0
rpcport=3889
port=8003
listen=1
staking=1
reservebalance=0
rpcallowip=127.0.0.1
dbcache=16384
par=8
checklevel=0
txindex=1
maxconnections=100
mempoolexpiry=7200
maxmempool=300
minrelaytxfee=0.00001
addrindex=1
spentindex=1
blockfilterindex=1
maxorphantx=100
blocksonly=0
limitfreerelay=0.0001
whitelistrelay=127.0.0.1
whitelistforcerelay=127.0.0.1
aggressive-staking=1
debug = 0
addnode=212.34.129.167:8003
addnode=212.34.144.140:8003
addnode=5.45.127.191:8004
addnode=45.92.176.61:38100
addnode=65.109.8.204:38100
addnode=185.157.214.191:38100
addnode=193.24.209.184:38100
addnode=185.250.207.235:38100
rescan=1
reindex=1
reindex-chainstate=1
deleteblockchaindata=1
`
	settings := optimizedNodeSettings(systemResources{CPUCount: 8, MemoryBytes: 20 * 1024 * 1024 * 1024})
	settings = append(settings,
		nodeConfigSetting{Key: "rpcuser", Value: "operator"},
		nodeConfigSetting{Key: "rpcpassword", Value: "secret", Secret: true},
	)
	updated, _, _, _ := rewriteNodeConfig(original, settings)
	updated, _ = ensureNodeConfigValue(updated, "addnode", "127.0.0.1:3890")

	for _, expected := range []string{
		"port=38100\n",
		"dbcache=4096\n",
		"par=7\n",
		"checklevel=3\n",
		"rescan=0\n",
		"reindex=0\n",
		"reindex-chainstate=0\n",
		"deleteblockchaindata=0\n",
		"rpcbind=127.0.0.1\n",
		"rpcuser=operator\n",
		"rpcpassword=secret\n",
		"addnode=127.0.0.1:3890\n",
	} {
		if !strings.Contains(updated, expected) {
			t.Fatalf("adapted configuration is missing %q:\n%s", expected, updated)
		}
	}
	for _, preserved := range []string{
		"staking=1\n",
		"reservebalance=0\n",
		"mempoolexpiry=7200\n",
		"minrelaytxfee=0.00001\n",
		"spentindex=1\n",
		"blockfilterindex=1\n",
		"limitfreerelay=0.0001\n",
		"whitelistrelay=127.0.0.1\n",
		"whitelistforcerelay=127.0.0.1\n",
		"aggressive-staking=1\n",
		"debug = 0\n",
		"addnode=212.34.129.167:8003\n",
		"addnode=185.250.207.235:38100\n",
	} {
		if !strings.Contains(updated, preserved) {
			t.Fatalf("operator setting was lost: %q\n%s", preserved, updated)
		}
	}
	if strings.Count(updated, "addnode=") != 9 {
		t.Fatalf("expected all eight legacy peers plus local proxy peer:\n%s", updated)
	}
}

func settingValues(settings []nodeConfigSetting) map[string]string {
	values := make(map[string]string, len(settings))
	for _, setting := range settings {
		values[setting.Key] = setting.Value
	}
	return values
}

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFreshMacEnvironment(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(dataDirVariable, "")
	t.Setenv(nodeDirVariable, "")
	env := map[string]string{"HOME": home}
	data, err := defaultDataDir("darwin", env)
	if err != nil {
		t.Fatal(err)
	}
	node := filepath.Join(home, "Applications")
	if err := ensureEnvironmentVariables("darwin", env, data, node); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(data, "zhcash-env")
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{data, node} {
		if !strings.Contains(string(before), value) {
			t.Fatalf("missing path %q", value)
		}
	}
	if _, err := cleanBlockchainData(data); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("cleanup changed environment")
	}
	if !preserveSnapshotTarget("zhcash-env") {
		t.Fatal("snapshot must not replace local environment")
	}
	// Finder does not source shell profiles: a second run has no ZHCASH variables.
	if err := ensureEnvironmentVariables("darwin", env, data, node); err != nil {
		t.Fatal(err)
	}
	profile, err := os.ReadFile(filepath.Join(home, ".zprofile"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(profile), "&& .") != 1 {
		t.Fatal("duplicate profile entries")
	}
}

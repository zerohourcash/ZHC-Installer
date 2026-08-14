package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"strings"
	"testing"
)

func TestDeriveMegaCryptoParams(t *testing.T) {
	params, err := deriveMegaCryptoParams("8avoKJxzjLjfgj2SbhBrqMo-FCqt-i2myM1XQZy49Gg")
	if err != nil {
		t.Fatal(err)
	}
	if got := bytesToHex(params.Key); got != "3b95fc023189a11e174f6ad3f2a89fc0" {
		t.Fatalf("unexpected AES key: %s", got)
	}
	if got := bytesToHex(params.IV); got != "ca3e142aadfa2da60000000000000000" {
		t.Fatalf("unexpected AES IV: %s", got)
	}
}

func TestNewCTRAtOffsetMatchesFullStreamSlice(t *testing.T) {
	key := []byte("0123456789abcdef")
	iv := []byte("abcdef9876543210")
	plain := bytes.Repeat([]byte("ZHCASH"), 100)

	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	encrypted := append([]byte(nil), plain...)
	cipher.NewCTR(block, iv).XORKeyStream(encrypted, encrypted)

	const offset = 37
	want := plain[offset:]
	got := append([]byte(nil), encrypted[offset:]...)
	stream := newCTRAtOffset(block, iv, offset)
	stream.XORKeyStream(got, got)

	if !bytes.Equal(got, want) {
		t.Fatal("offset CTR decryption did not match full stream slice")
	}
}

func TestResolveSourcesAutoOrder(t *testing.T) {
	sources, err := resolveSources("auto")
	if err != nil {
		t.Fatal(err)
	}
	if got := sourceNames(sources); !bytes.Equal([]byte(got), []byte("mega,yandex,zeroscan")) {
		t.Fatalf("unexpected auto source order: %s", got)
	}
}

func TestResolveSourcesSingleSource(t *testing.T) {
	t.Setenv(yandexURLVariable, "https://example.invalid/secret-yandex-link")

	sources, err := resolveSources("yandex")
	if err != nil {
		t.Fatal(err)
	}
	if got := sourceNames(sources); got != "yandex" {
		t.Fatalf("unexpected single source: %s", got)
	}
	if sources[0].URL != "https://example.invalid/secret-yandex-link" {
		t.Fatal("expected yandex URL to be read from environment")
	}
}

func TestConfiguredYandexURLUsesObfuscatedPayloadBeforeEnvironment(t *testing.T) {
	oldPayload, oldKey := yandexURLPayload, yandexURLKey
	defer func() {
		yandexURLPayload, yandexURLKey = oldPayload, oldKey
	}()

	t.Setenv(yandexURLVariable, "https://example.invalid/env-link")
	payload, key := obfuscateForTest("https://example.invalid/embedded-link", "test-key")
	yandexURLPayload = payload
	yandexURLKey = key

	if got := configuredYandexURL(); got != "https://example.invalid/embedded-link" {
		t.Fatalf("unexpected configured yandex URL: %s", got)
	}
}

func TestConfiguredYandexURLFallsBackToEnvironment(t *testing.T) {
	oldPayload, oldKey := yandexURLPayload, yandexURLKey
	defer func() {
		yandexURLPayload, yandexURLKey = oldPayload, oldKey
	}()

	yandexURLPayload = ""
	yandexURLKey = ""
	t.Setenv(yandexURLVariable, "https://example.invalid/env-link")

	if got := configuredYandexURL(); got != "https://example.invalid/env-link" {
		t.Fatalf("unexpected configured yandex URL: %s", got)
	}
}

func TestResolveSourcesRejectsUnknownSource(t *testing.T) {
	if _, err := resolveSources("unknown"); err == nil {
		t.Fatal("expected unknown source to be rejected")
	}
}

func sourceNames(sources []sourceConfig) string {
	names := make([]string, len(sources))
	for i, source := range sources {
		names[i] = source.Name
	}
	return strings.Join(names, ",")
}

func obfuscateForTest(value string, key string) (string, string) {
	data := []byte(value)
	keyBytes := []byte(key)
	for i := range data {
		data[i] ^= keyBytes[i%len(keyBytes)]
	}
	return base64.StdEncoding.EncodeToString(data), base64.StdEncoding.EncodeToString(keyBytes)
}

func bytesToHex(data []byte) string {
	const hex = "0123456789abcdef"
	out := make([]byte, len(data)*2)
	for i, b := range data {
		out[i*2] = hex[b>>4]
		out[i*2+1] = hex[b&0x0f]
	}
	return string(out)
}

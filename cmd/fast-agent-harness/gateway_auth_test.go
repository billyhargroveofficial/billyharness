package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/gatewayauth"
)

func TestResolveGatewayServeAuthExplicitTokenHasHighestPriorityAndIsNotPersisted(t *testing.T) {
	home := isolateGatewayAuthTest(t)
	t.Setenv(gatewayauth.PrimaryEnv, "process-env-token")

	got, err := resolveGatewayServeAuth("  explicit-operator-token  ", true, false)
	if err != nil {
		t.Fatalf("resolve explicit gateway auth: %v", err)
	}
	if got.Token != "explicit-operator-token" {
		t.Fatalf("token = %q, want explicit operator token", got.Token)
	}
	if got.GeneratedPath != "" {
		t.Fatalf("generated path = %q, want empty for explicit token", got.GeneratedPath)
	}
	assertPathMissing(t, filepath.Join(home, "auth", "gateway.token"))
}

func TestResolveGatewayServeAuthLoopbackGeneratesAndReusesDedicatedToken(t *testing.T) {
	home := isolateGatewayAuthTest(t)
	tokenPath := filepath.Join(home, "auth", "gateway.token")

	first, err := resolveGatewayServeAuth("", false, false)
	if err != nil {
		t.Fatalf("first loopback auth resolution: %v", err)
	}
	if first.Token == "" {
		t.Fatal("first loopback auth token is empty")
	}
	if first.GeneratedPath != tokenPath {
		t.Fatalf("first generated path = %q, want %q", first.GeneratedPath, tokenPath)
	}

	persisted, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatalf("read generated token: %v", err)
	}
	if strings.TrimSpace(string(persisted)) != first.Token {
		t.Fatal("persisted token does not match resolved token")
	}
	assertOwnerOnlyRegularFile(t, tokenPath)
	assertOwnerOnlyDirectory(t, filepath.Dir(tokenPath))

	second, err := resolveGatewayServeAuth("", false, false)
	if err != nil {
		t.Fatalf("second loopback auth resolution: %v", err)
	}
	if second.Token != first.Token {
		t.Fatal("second loopback start did not reuse the dedicated token")
	}
	if second.GeneratedPath != "" {
		t.Fatalf("second generated path = %q, want empty for a reused token", second.GeneratedPath)
	}
}

func TestResolveGatewayServeAuthLoopbackDevelopmentBypassDoesNotCreateToken(t *testing.T) {
	home := isolateGatewayAuthTest(t)

	got, err := resolveGatewayServeAuth("", false, true)
	if err != nil {
		t.Fatalf("resolve loopback development bypass: %v", err)
	}
	if got.Token != "" || got.GeneratedPath != "" {
		t.Fatalf("development bypass result = %#v, want no token or path", got)
	}
	assertPathMissing(t, filepath.Join(home, "auth", "gateway.token"))
}

func TestResolveGatewayServeAuthNonLoopbackMissingTokenFailsClosed(t *testing.T) {
	home := isolateGatewayAuthTest(t)

	got, err := resolveGatewayServeAuth("", true, false)
	if err == nil {
		t.Fatal("non-loopback auth resolution unexpectedly succeeded without a token")
	}
	if got.Token != "" || got.GeneratedPath != "" {
		t.Fatalf("failed non-loopback result = %#v, want no token or path", got)
	}
	assertPathMissing(t, filepath.Join(home, "auth", "gateway.token"))
}

func TestResolveGatewayServeAuthNonLoopbackUsesPreprovisionedDedicatedToken(t *testing.T) {
	home := isolateGatewayAuthTest(t)
	tokenPath := filepath.Join(home, "auth", "gateway.token")
	writeGatewayAuthTestToken(t, tokenPath, "preprovisioned-token", 0o600)

	got, err := resolveGatewayServeAuth("", true, false)
	if err != nil {
		t.Fatalf("resolve preprovisioned non-loopback auth: %v", err)
	}
	if got.Token != "preprovisioned-token" {
		t.Fatalf("token = %q, want preprovisioned token", got.Token)
	}
	if got.GeneratedPath != "" {
		t.Fatalf("generated path = %q, want empty for preprovisioned token", got.GeneratedPath)
	}
}

func TestResolveGatewayServeAuthMigratesHomeDotenvFallback(t *testing.T) {
	home := isolateGatewayAuthTest(t)
	dotenvPath := filepath.Join(home, ".env")
	if err := os.WriteFile(dotenvPath, []byte(gatewayauth.PrimaryEnv+"=home-dotenv-token\n"), 0o600); err != nil {
		t.Fatalf("write home dotenv: %v", err)
	}

	got, err := resolveGatewayServeAuth("", false, false)
	if err != nil {
		t.Fatalf("resolve home dotenv fallback: %v", err)
	}
	if got.Token != "home-dotenv-token" {
		t.Fatalf("token = %q, want migrated home dotenv token", got.Token)
	}
	if got.GeneratedPath != "" {
		t.Fatalf("generated path = %q, want empty for migrated token", got.GeneratedPath)
	}

	tokenPath := filepath.Join(home, "auth", "gateway.token")
	persisted, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatalf("read migrated dedicated token: %v", err)
	}
	if strings.TrimSpace(string(persisted)) != "home-dotenv-token" {
		t.Fatal("dedicated token does not contain the migrated home dotenv value")
	}
	assertOwnerOnlyRegularFile(t, tokenPath)
}

func TestResolveGatewayServeAuthRejectsUnsafeStoreWithoutLeakingSecret(t *testing.T) {
	t.Run("corrupt token", func(t *testing.T) {
		home := isolateGatewayAuthTest(t)
		tokenPath := filepath.Join(home, "auth", "gateway.token")
		secret := "corrupt-secret-marker"
		writeGatewayAuthTestToken(t, tokenPath, secret+"\x00suffix", 0o600)

		got, err := resolveGatewayServeAuth("", false, false)
		assertGatewayAuthSecretSafeFailure(t, got, err, secret)
	})

	t.Run("insecure permissions", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("owner-only POSIX mode enforcement is not available on Windows")
		}
		home := isolateGatewayAuthTest(t)
		tokenPath := filepath.Join(home, "auth", "gateway.token")
		secret := "permission-secret-marker"
		writeGatewayAuthTestToken(t, tokenPath, secret, 0o644)

		got, err := resolveGatewayServeAuth("", false, false)
		assertGatewayAuthSecretSafeFailure(t, got, err, secret)
	})
}

func isolateGatewayAuthTest(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	t.Setenv("FAST_AGENT_ENV_FILE", "")
	t.Setenv(gatewayauth.PrimaryEnv, "")
	t.Setenv(gatewayauth.LegacyEnv, "")
	return home
}

func writeGatewayAuthTestToken(t *testing.T, path, token string, mode fs.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create gateway auth test directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(token+"\n"), mode); err != nil {
		t.Fatalf("write gateway auth test token: %v", err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("set gateway auth test token mode: %v", err)
	}
}

func assertGatewayAuthSecretSafeFailure(t *testing.T, got gatewayServeAuth, err error, secret string) {
	t.Helper()
	if err == nil {
		t.Fatal("unsafe gateway auth store unexpectedly succeeded")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("gateway auth error leaked secret %q: %v", secret, err)
	}
	if got.Token != "" || got.GeneratedPath != "" {
		t.Fatalf("unsafe store result = %#v, want no token or path", got)
	}
}

func assertPathMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("path %s exists or cannot be inspected: %v", path, err)
	}
}

func assertOwnerOnlyRegularFile(t *testing.T, path string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("inspect %s: %v", path, err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("%s mode = %v, want regular file", path, info.Mode())
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("%s mode = %04o, want owner-only", path, info.Mode().Perm())
	}
}

func assertOwnerOnlyDirectory(t *testing.T, path string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("inspect %s: %v", path, err)
	}
	if !info.IsDir() {
		t.Fatalf("%s mode = %v, want directory", path, info.Mode())
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("%s mode = %04o, want owner-only", path, info.Mode().Perm())
	}
}

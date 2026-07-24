package gatewayauth

import (
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestResolvePrecedence(t *testing.T) {
	home := newTestHome(t)
	authDir := filepath.Join(home, "auth")
	mustMkdirAll(t, authDir, 0o700)
	mustWriteFile(t, DefaultPath(), "dedicated-token\n", 0o600)
	dotenvPath := filepath.Join(home, ".env")
	mustWriteFile(t, dotenvPath, PrimaryEnv+"=primary-home-token\n"+LegacyEnv+"=legacy-home-token\n", 0o600)
	t.Setenv(PrimaryEnv, " primary-process-token ")
	t.Setenv(LegacyEnv, " legacy-process-token ")

	assertResolved(t, Result{
		Value:  "primary-process-token",
		Source: SourcePrimaryProcessEnv,
	})

	t.Setenv(PrimaryEnv, "")
	assertResolved(t, Result{
		Value:  "dedicated-token",
		Source: SourceDedicatedFile,
		Path:   DefaultPath(),
	})

	if err := os.Remove(DefaultPath()); err != nil {
		t.Fatal(err)
	}
	assertResolved(t, Result{
		Value:  "primary-home-token",
		Source: SourcePrimaryHomeDotenv,
		Path:   dotenvPath,
	})

	mustWriteFile(t, dotenvPath, LegacyEnv+"=legacy-home-token\n", 0o600)
	assertResolved(t, Result{
		Value:  "legacy-process-token",
		Source: SourceLegacyProcessEnv,
	})

	t.Setenv(LegacyEnv, "")
	assertResolved(t, Result{
		Value:  "legacy-home-token",
		Source: SourceLegacyHomeDotenv,
		Path:   dotenvPath,
	})
}

func TestResolveIgnoresProjectAndExplicitDotenvFiles(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	workdir := filepath.Join(root, "project")
	explicit := filepath.Join(root, "explicit.env")
	mustMkdirAll(t, home, 0o700)
	mustMkdirAll(t, workdir, 0o700)
	mustWriteFile(t, filepath.Join(workdir, ".env"), PrimaryEnv+"=project-token\n"+LegacyEnv+"=project-legacy-token\n", 0o600)
	mustWriteFile(t, explicit, PrimaryEnv+"=explicit-token\n"+LegacyEnv+"=explicit-legacy-token\n", 0o600)

	t.Setenv("BILLYHARNESS_HOME", home)
	t.Setenv("FAST_AGENT_ENV_FILE", explicit)
	t.Setenv(PrimaryEnv, "")
	t.Setenv(LegacyEnv, "")
	t.Chdir(workdir)

	got, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if got.Value != "" || got.Source != "" || got.Path != filepath.Join(home, "auth", "gateway.token") {
		t.Fatalf("Resolve unexpectedly used a project or explicit dotenv source: source=%q path=%q value_present=%v", got.Source, got.Path, got.Value != "")
	}
}

func TestEnsureGeneratesPrivateTokenAndReusesIt(t *testing.T) {
	home := newTestHome(t)

	first, err := Ensure()
	if err != nil {
		t.Fatal(err)
	}
	if first.Source != SourceGenerated || !first.Created || first.Path != DefaultPath() {
		t.Fatalf("first Ensure metadata = source:%q created:%v path:%q", first.Source, first.Created, first.Path)
	}
	if !strings.HasPrefix(first.Value, generatedPrefix) {
		t.Fatalf("generated token is missing the Billyharness gateway prefix")
	}
	raw, err := hex.DecodeString(strings.TrimPrefix(first.Value, generatedPrefix))
	if err != nil || len(raw) != 32 {
		t.Fatalf("generated token does not contain 256 random bits")
	}

	body, err := os.ReadFile(first.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != first.Value+"\n" {
		t.Fatal("dedicated token file does not contain the generated token and one trailing newline")
	}
	assertPrivateMode(t, filepath.Join(home, "auth"), 0o700)
	assertPrivateMode(t, first.Path, 0o600)

	second, err := Ensure()
	if err != nil {
		t.Fatal(err)
	}
	if second.Value != first.Value || second.Source != SourceDedicatedFile || second.Created || second.Path != first.Path {
		t.Fatalf("second Ensure did not reuse the dedicated token: source=%q created=%v path=%q same_value=%v", second.Source, second.Created, second.Path, second.Value == first.Value)
	}
}

func TestConcurrentEnsureConvergesOnOneToken(t *testing.T) {
	newTestHome(t)

	const callers = 32
	results := make([]Result, callers)
	errs := make([]error, callers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(callers)
	for i := range callers {
		go func() {
			defer wg.Done()
			<-start
			results[i], errs[i] = Ensure()
		}()
	}
	close(start)
	wg.Wait()

	created := 0
	for i := range callers {
		if errs[i] != nil {
			t.Fatalf("Ensure caller %d: %v", i, errs[i])
		}
		if results[i].Value != results[0].Value {
			t.Fatalf("Ensure caller %d did not converge on the winning token", i)
		}
		if results[i].Path != DefaultPath() {
			t.Fatalf("Ensure caller %d path = %q", i, results[i].Path)
		}
		if results[i].Created {
			created++
			if results[i].Source != SourceGenerated {
				t.Fatalf("creator source = %q", results[i].Source)
			}
		} else if results[i].Source != SourceDedicatedFile {
			t.Fatalf("non-creator source = %q", results[i].Source)
		}
	}
	if created != 1 {
		t.Fatalf("created count = %d, want 1", created)
	}
}

func TestConcurrentProcessesEnsureConvergeOnOneToken(t *testing.T) {
	if !hasUnixSecurityChecks() {
		t.Skip("cross-process store locking is supported on Darwin and Linux")
	}
	home := newTestHome(t)

	const callers = 12
	outputPaths := make([]string, callers)
	errs := make([]error, callers)
	outputs := make([][]byte, callers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(callers)
	for index := range callers {
		outputPaths[index] = filepath.Join(t.TempDir(), "result")
		go func() {
			defer wg.Done()
			<-start
			command := exec.Command(os.Args[0], "-test.run=^TestEnsureSubprocessHelper$", "-test.count=1")
			command.Env = append(os.Environ(),
				"BILLYHARNESS_GATEWAYAUTH_HELPER=1",
				"BILLYHARNESS_GATEWAYAUTH_RESULT="+outputPaths[index],
				"BILLYHARNESS_HOME="+home,
				PrimaryEnv+"=",
				LegacyEnv+"=",
				"FAST_AGENT_ENV_FILE=",
			)
			outputs[index], errs[index] = command.CombinedOutput()
		}()
	}
	close(start)
	wg.Wait()

	var winner string
	for index := range callers {
		if errs[index] != nil {
			t.Fatalf("Ensure subprocess %d: %v\n%s", index, errs[index], outputs[index])
		}
		body, err := os.ReadFile(outputPaths[index])
		if err != nil {
			t.Fatalf("read Ensure subprocess %d result: %v", index, err)
		}
		value := strings.TrimSpace(string(body))
		if value == "" {
			t.Fatalf("Ensure subprocess %d returned an empty token", index)
		}
		if winner == "" {
			winner = value
		} else if value != winner {
			t.Fatalf("Ensure subprocess %d did not converge on the winning token", index)
		}
	}

	resolved, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Source != SourceDedicatedFile || resolved.Value != winner {
		t.Fatalf("final dedicated token does not match subprocess winner")
	}
}

func TestEnsureSubprocessHelper(t *testing.T) {
	if os.Getenv("BILLYHARNESS_GATEWAYAUTH_HELPER") != "1" {
		return
	}
	result, err := Ensure()
	if err != nil {
		t.Fatal(err)
	}
	path := os.Getenv("BILLYHARNESS_GATEWAYAUTH_RESULT")
	if path == "" {
		t.Fatal("missing helper result path")
	}
	if err := os.WriteFile(path, []byte(result.Value+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestEnsurePrimaryProcessEnvIsNotPersisted(t *testing.T) {
	newTestHome(t)
	t.Setenv(PrimaryEnv, "explicit-primary-token")

	got, err := Ensure()
	if err != nil {
		t.Fatal(err)
	}
	if got.Value != "explicit-primary-token" || got.Source != SourcePrimaryProcessEnv || got.Path != "" || got.Created {
		t.Fatalf("Ensure primary override metadata = source:%q created:%v path:%q value_matches:%v", got.Source, got.Created, got.Path, got.Value == "explicit-primary-token")
	}
	if _, err := os.Lstat(DefaultPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("primary process override was persisted: %v", err)
	}
}

func TestEnsureMigratesFallbackSources(t *testing.T) {
	tests := []struct {
		name   string
		source string
		setup  func(t *testing.T, home, value string)
	}{
		{
			name:   "primary home dotenv",
			source: SourcePrimaryHomeDotenv,
			setup: func(t *testing.T, home, value string) {
				mustWriteFile(t, filepath.Join(home, ".env"), PrimaryEnv+"="+value+"\n", 0o600)
			},
		},
		{
			name:   "legacy process env",
			source: SourceLegacyProcessEnv,
			setup: func(t *testing.T, _, value string) {
				t.Setenv(LegacyEnv, value)
			},
		},
		{
			name:   "legacy home dotenv",
			source: SourceLegacyHomeDotenv,
			setup: func(t *testing.T, home, value string) {
				mustWriteFile(t, filepath.Join(home, ".env"), LegacyEnv+"="+value+"\n", 0o600)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := newTestHome(t)
			value := "migration-token-" + strings.ReplaceAll(tt.name, " ", "-")
			tt.setup(t, home, value)

			got, err := Ensure()
			if err != nil {
				t.Fatal(err)
			}
			if got.Value != value || got.Source != tt.source || got.Path != DefaultPath() || got.Created {
				t.Fatalf("Ensure migration metadata = source:%q created:%v path:%q value_matches:%v", got.Source, got.Created, got.Path, got.Value == value)
			}
			body, err := os.ReadFile(DefaultPath())
			if err != nil {
				t.Fatal(err)
			}
			if string(body) != value+"\n" {
				t.Fatal("migrated dedicated file does not contain the fallback token")
			}
			assertPrivateMode(t, filepath.Join(home, "auth"), 0o700)
			assertPrivateMode(t, DefaultPath(), 0o600)

			resolved, err := Resolve()
			if err != nil {
				t.Fatal(err)
			}
			if resolved.Value != value || resolved.Source != SourceDedicatedFile || resolved.Path != DefaultPath() {
				t.Fatalf("post-migration Resolve = source:%q path:%q value_matches:%v", resolved.Source, resolved.Path, resolved.Value == value)
			}
		})
	}
}

func TestBillyHomesHaveIsolatedDedicatedTokens(t *testing.T) {
	root := t.TempDir()
	homeA := filepath.Join(root, "a")
	homeB := filepath.Join(root, "b")
	t.Setenv(PrimaryEnv, "")
	t.Setenv(LegacyEnv, "")
	t.Setenv("FAST_AGENT_ENV_FILE", "")

	t.Setenv("BILLYHARNESS_HOME", homeA)
	tokenA, err := Ensure()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("BILLYHARNESS_HOME", homeB)
	tokenB, err := Ensure()
	if err != nil {
		t.Fatal(err)
	}
	if tokenA.Value == tokenB.Value || tokenA.Path == tokenB.Path {
		t.Fatal("different BILLYHARNESS_HOME values did not produce isolated token stores")
	}

	t.Setenv("BILLYHARNESS_HOME", homeA)
	again, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if again.Value != tokenA.Value || again.Path != tokenA.Path || again.Source != SourceDedicatedFile {
		t.Fatal("switching back to the first BILLYHARNESS_HOME did not recover its token")
	}
}

func TestResolveRejectsInvalidAndUnsafeDedicatedTokens(t *testing.T) {
	t.Run("control characters", func(t *testing.T) {
		newTestHome(t)
		writeDedicatedToken(t, "do-not-leak-control-secret\nembedded\n", 0o600)
		err := resolveError(t)
		assertErrorContains(t, err, "visible ASCII")
		assertErrorOmits(t, err, "do-not-leak-control-secret")
	})

	t.Run("oversized", func(t *testing.T) {
		newTestHome(t)
		secret := "do-not-leak-oversized-secret"
		writeDedicatedToken(t, secret+strings.Repeat("x", maxTokenFile+1), 0o600)
		err := resolveError(t)
		assertErrorContains(t, err, "exceeds the secure size limit")
		assertErrorOmits(t, err, secret)
	})

	t.Run("insecure permissions", func(t *testing.T) {
		if !hasUnixSecurityChecks() {
			t.Skip("owner-only mode enforcement is Unix-specific")
		}
		newTestHome(t)
		secret := "do-not-leak-insecure-secret"
		writeDedicatedToken(t, secret+"\n", 0o600)
		if err := os.Chmod(DefaultPath(), 0o644); err != nil {
			t.Fatal(err)
		}
		err := resolveError(t)
		assertErrorContains(t, err, "not owner-only")
		assertErrorOmits(t, err, secret)
	})

	t.Run("insecure auth directory permissions", func(t *testing.T) {
		if !hasUnixSecurityChecks() {
			t.Skip("owner-only mode enforcement is Unix-specific")
		}
		home := newTestHome(t)
		authDir := filepath.Join(home, "auth")
		mustMkdirAll(t, authDir, 0o755)
		secret := "do-not-leak-insecure-directory-secret"
		mustWriteFile(t, DefaultPath(), secret+"\n", 0o600)
		err := resolveError(t)
		assertErrorContains(t, err, "auth directory permissions")
		assertErrorOmits(t, err, secret)
	})

	t.Run("token symlink", func(t *testing.T) {
		home := newTestHome(t)
		authDir := filepath.Join(home, "auth")
		mustMkdirAll(t, authDir, 0o700)
		target := filepath.Join(home, "real-token")
		secret := "do-not-leak-symlink-secret"
		mustWriteFile(t, target, secret+"\n", 0o600)
		if err := os.Symlink(target, DefaultPath()); err != nil {
			t.Fatal(err)
		}
		err := resolveError(t)
		assertErrorContains(t, err, "refusing symlink")
		assertErrorOmits(t, err, secret)
	})

	t.Run("auth directory symlink", func(t *testing.T) {
		home := newTestHome(t)
		realAuth := filepath.Join(filepath.Dir(home), "real-auth")
		mustMkdirAll(t, realAuth, 0o700)
		secret := "do-not-leak-auth-dir-symlink-secret"
		mustWriteFile(t, filepath.Join(realAuth, "gateway.token"), secret+"\n", 0o600)
		if err := os.Symlink(realAuth, filepath.Join(home, "auth")); err != nil {
			t.Fatal(err)
		}
		err := resolveError(t)
		assertErrorContains(t, err, "not a real directory")
		assertErrorOmits(t, err, secret)
	})

	t.Run("hard link", func(t *testing.T) {
		if !hasUnixSecurityChecks() {
			t.Skip("single-link enforcement is Unix-specific")
		}
		home := newTestHome(t)
		authDir := filepath.Join(home, "auth")
		mustMkdirAll(t, authDir, 0o700)
		target := filepath.Join(home, "original-token")
		secret := "do-not-leak-hardlink-secret"
		mustWriteFile(t, target, secret+"\n", 0o600)
		if err := os.Link(target, DefaultPath()); err != nil {
			t.Fatal(err)
		}
		err := resolveError(t)
		assertErrorContains(t, err, "multiply-linked")
		assertErrorOmits(t, err, secret)
	})
}

func TestResolveMissingDedicatedTokenIgnoresExistingAuthDirectory(t *testing.T) {
	home := newTestHome(t)
	// The auth directory may legitimately preexist for codex.json or
	// credentials.json. Without gateway.token it is not yet a managed store.
	mustMkdirAll(t, filepath.Join(home, "auth"), 0o755)

	got, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve with unrelated existing auth directory: %v", err)
	}
	if got.Value != "" || got.Source != "" {
		t.Fatalf("Resolve unexpectedly found a gateway token: %#v", got)
	}
}

func TestResolveRejectsSymlinkedBillyHome(t *testing.T) {
	root := t.TempDir()
	realHome := filepath.Join(root, "real-home")
	mustMkdirAll(t, filepath.Join(realHome, "auth"), 0o700)
	secret := "do-not-leak-symlinked-home-secret"
	mustWriteFile(t, filepath.Join(realHome, "auth", "gateway.token"), secret+"\n", 0o600)
	linkHome := filepath.Join(root, "linked-home")
	if err := os.Symlink(realHome, linkHome); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BILLYHARNESS_HOME", linkHome)
	t.Setenv(PrimaryEnv, "")
	t.Setenv(LegacyEnv, "")

	err := resolveError(t)
	assertErrorContains(t, err, "not a real directory")
	assertErrorOmits(t, err, secret)
}

func TestDefaultPathMakesRelativeBillyHomeAbsolute(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("BILLYHARNESS_HOME", "relative-home")

	got := DefaultPath()
	want := filepath.Join(root, "relative-home", "auth", "gateway.token")
	if got != want || !filepath.IsAbs(got) {
		t.Fatalf("DefaultPath() = %q, want absolute %q", got, want)
	}
}

func TestValidateTokenRejectsWhitespaceAndNonASCII(t *testing.T) {
	for _, value := range []string{"two words", "tab\tinside", "unicode-ключ"} {
		if _, err := ValidateToken(value); err == nil {
			t.Fatalf("ValidateToken(%q) unexpectedly succeeded", value)
		}
	}
}

func TestResolveRejectsInvalidProcessTokensWithoutLeakingThem(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
		want  string
	}{
		{
			name:  "primary control character",
			key:   PrimaryEnv,
			value: "do-not-leak-primary-secret\nfragment",
			want:  "visible ASCII",
		},
		{
			name:  "legacy control character",
			key:   LegacyEnv,
			value: "do-not-leak-legacy-secret\nfragment",
			want:  "visible ASCII",
		},
		{
			name:  "primary oversized",
			key:   PrimaryEnv,
			value: "do-not-leak-primary-oversized-" + strings.Repeat("x", maxTokenBytes),
			want:  "exceeds the secure size limit",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			newTestHome(t)
			t.Setenv(tt.key, tt.value)
			err := resolveError(t)
			assertErrorContains(t, err, tt.want)
			assertErrorOmits(t, err, strings.Split(tt.value, "\n")[0])
		})
	}
}

func newTestHome(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "billyhome")
	mustMkdirAll(t, home, 0o700)
	t.Setenv("BILLYHARNESS_HOME", home)
	t.Setenv("FAST_AGENT_ENV_FILE", "")
	t.Setenv(PrimaryEnv, "")
	t.Setenv(LegacyEnv, "")
	return home
}

func writeDedicatedToken(t *testing.T, body string, mode os.FileMode) {
	t.Helper()
	mustMkdirAll(t, filepath.Dir(DefaultPath()), 0o700)
	mustWriteFile(t, DefaultPath(), body, mode)
}

func mustMkdirAll(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(path, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func mustWriteFile(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func assertResolved(t *testing.T, want Result) {
	t.Helper()
	got, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if got.Value != want.Value || got.Source != want.Source || got.Path != want.Path || got.Created != want.Created {
		t.Fatalf("Resolve = source:%q path:%q created:%v value_matches:%v; want source:%q path:%q created:%v", got.Source, got.Path, got.Created, got.Value == want.Value, want.Source, want.Path, want.Created)
	}
}

func assertPrivateMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	if !hasUnixSecurityChecks() {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %#o, want %#o", path, got, want)
	}
}

func resolveError(t *testing.T) error {
	t.Helper()
	_, err := Resolve()
	if err == nil {
		t.Fatal("Resolve succeeded, want rejection")
	}
	return err
}

func assertErrorContains(t *testing.T, err error, want string) {
	t.Helper()
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not contain %q", err, want)
	}
}

func assertErrorOmits(t *testing.T, err error, secret string) {
	t.Helper()
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked secret material")
	}
}

func hasUnixSecurityChecks() bool {
	return runtime.GOOS == "darwin" || runtime.GOOS == "linux"
}

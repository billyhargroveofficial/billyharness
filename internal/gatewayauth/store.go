// Package gatewayauth resolves and securely persists the local gateway bearer
// token. It deliberately ignores project dotenv discovery and explicit dotenv
// overrides: the only migration input is $BILLYHARNESS_HOME/.env.
package gatewayauth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/billyhargroveofficial/billyharness/internal/config"
)

const (
	PrimaryEnv = "BILLYHARNESS_GATEWAY_AUTH_TOKEN"
	LegacyEnv  = "FAST_AGENT_GATEWAY_AUTH_TOKEN"

	SourcePrimaryProcessEnv = "primary_process_env"
	SourceDedicatedFile     = "dedicated_file"
	SourcePrimaryHomeDotenv = "primary_home_dotenv"
	SourceLegacyProcessEnv  = "legacy_process_env"
	SourceLegacyHomeDotenv  = "legacy_home_dotenv"
	SourceGenerated         = "generated"

	maxTokenBytes   = 4 << 10
	maxTokenFile    = maxTokenBytes + 2
	maxDotenvBytes  = 1 << 20
	generatedPrefix = "bgh_"
)

// Result contains a token and bounded provenance. Value is secret and must not
// be logged. Path is empty for process environment sources. Ensure returns the
// canonical token path after a successful migration or generation. Created is
// true only when Ensure generated a new random token, not when it migrated one.
type Result struct {
	Value   string
	Source  string
	Path    string
	Created bool
}

var ensureMu sync.Mutex

// DefaultPath is the canonical dedicated token path for the active Billy home.
func DefaultPath() string {
	home := config.BillyHomeDir()
	if absolute, err := filepath.Abs(home); err == nil {
		home = absolute
	}
	return filepath.Join(home, "auth", "gateway.token")
}

// Resolve returns the first configured token without creating or modifying
// files. Precedence is the primary process environment, the dedicated file,
// the primary key in the home dotenv (migration compatibility), the legacy
// process environment, and finally the legacy key in the home dotenv.
func Resolve() (Result, error) {
	ensureMu.Lock()
	defer ensureMu.Unlock()
	return resolve()
}

// Ensure resolves an existing token, migrates a fallback into the dedicated
// file, or generates and atomically publishes a random 256-bit token.
// Concurrent first starts converge under a cross-process store lock on the
// supported Unix production platforms.
func Ensure() (Result, error) {
	ensureMu.Lock()
	defer ensureMu.Unlock()

	resolved, err := resolve()
	if err != nil {
		return Result{}, err
	}
	if resolved.Source == SourcePrimaryProcessEnv || resolved.Source == SourceDedicatedFile {
		return resolved, nil
	}

	path := DefaultPath()
	if err := ensurePrivateAuthDir(filepath.Dir(path)); err != nil {
		return Result{}, err
	}

	lock, err := lockTokenStore(filepath.Dir(path))
	if err != nil {
		return Result{}, err
	}
	defer func() {
		_ = unlockTokenStore(lock)
	}()

	// Another process may have published while this process waited for the
	// store lock. Re-resolve under the lock before choosing or writing a value.
	resolved, err = resolve()
	if err != nil {
		return Result{}, err
	}
	if resolved.Source == SourcePrimaryProcessEnv || resolved.Source == SourceDedicatedFile {
		return resolved, nil
	}

	value := resolved.Value
	generated := false
	if value == "" {
		value, err = randomToken()
		if err != nil {
			return Result{}, fmt.Errorf("generate gateway token: %w", err)
		}
		generated = true
	}

	if err := publishTokenAtomic(path, value); err != nil {
		return Result{}, err
	}

	source := resolved.Source
	if generated {
		source = SourceGenerated
	}
	return Result{Value: value, Source: source, Path: path, Created: generated}, nil
}

func resolve() (Result, error) {
	if value := strings.TrimSpace(os.Getenv(PrimaryEnv)); value != "" {
		value, err := validateToken(value)
		if err != nil {
			return Result{}, fmt.Errorf("invalid %s: %w", PrimaryEnv, err)
		}
		return Result{Value: value, Source: SourcePrimaryProcessEnv}, nil
	}

	path := DefaultPath()
	value, found, err := readOptionalDedicatedToken(path)
	if err != nil {
		return Result{}, err
	}
	if found {
		return Result{Value: value, Source: SourceDedicatedFile, Path: path}, nil
	}

	dotenvPath := filepath.Join(filepath.Dir(filepath.Dir(path)), ".env")
	dotenv, foundDotenv, err := readOptionalSecureFile(dotenvPath, maxDotenvBytes, false)
	if err != nil {
		return Result{}, fmt.Errorf("read home dotenv: %w", err)
	}
	if foundDotenv {
		if value, ok, parseErr := dotenvToken(dotenv, PrimaryEnv); parseErr != nil {
			return Result{}, parseErr
		} else if ok {
			return Result{Value: value, Source: SourcePrimaryHomeDotenv, Path: dotenvPath}, nil
		}
	}

	if value := strings.TrimSpace(os.Getenv(LegacyEnv)); value != "" {
		value, err := validateToken(value)
		if err != nil {
			return Result{}, fmt.Errorf("invalid %s: %w", LegacyEnv, err)
		}
		return Result{Value: value, Source: SourceLegacyProcessEnv}, nil
	}

	if foundDotenv {
		if value, ok, parseErr := dotenvToken(dotenv, LegacyEnv); parseErr != nil {
			return Result{}, parseErr
		} else if ok {
			return Result{Value: value, Source: SourceLegacyHomeDotenv, Path: dotenvPath}, nil
		}
	}
	return Result{Path: path}, nil
}

func readOptionalDedicatedToken(path string) (string, bool, error) {
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		// An existing auth directory may hold unrelated credentials. Do not
		// require managed-token filesystem guarantees until gateway.token itself
		// exists; this also keeps the explicit Windows development bypass usable.
		return "", false, nil
	} else if err != nil {
		return "", false, fmt.Errorf("inspect dedicated gateway token %s: %w", path, err)
	}

	authDir := filepath.Dir(path)
	home := filepath.Dir(authDir)
	homeInfo, err := os.Lstat(home)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("inspect Billy home for gateway auth %s: %w", home, err)
	}
	if homeInfo.Mode()&os.ModeSymlink != 0 || !homeInfo.IsDir() {
		return "", false, fmt.Errorf("Billy home for gateway auth %s is not a real directory", home)
	}
	if err := validateDirectoryPlatform(homeInfo, home, false); err != nil {
		return "", false, err
	}

	info, err := os.Lstat(authDir)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("inspect gateway auth directory %s: %w", authDir, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", false, fmt.Errorf("gateway auth directory %s is not a real directory", authDir)
	}
	if err := validateDirectoryPlatform(info, authDir, true); err != nil {
		return "", false, err
	}

	body, found, err := readOptionalSecureFile(path, maxTokenFile, true)
	if err != nil {
		return "", false, fmt.Errorf("read dedicated gateway token %s: %w", path, err)
	}
	if !found {
		return "", false, nil
	}
	value, err := validateToken(string(body))
	if err != nil {
		return "", false, fmt.Errorf("invalid dedicated gateway token %s: %w", path, err)
	}
	return value, true, nil
}

func readOptionalSecureFile(path string, limit int64, requirePrivate bool) ([]byte, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("inspect %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, false, fmt.Errorf("refusing symlink at %s", path)
	}
	if !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("refusing non-regular file at %s", path)
	}

	file, err := openRegularNoFollow(path)
	if err != nil {
		return nil, false, err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, false, fmt.Errorf("inspect opened secure file %s: %w", path, err)
	}
	if !os.SameFile(info, openedInfo) {
		return nil, false, fmt.Errorf("secure file changed while opening %s", path)
	}
	if err := validateOpenedRegular(file, path, requirePrivate); err != nil {
		return nil, false, err
	}
	body, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, false, fmt.Errorf("read bounded file %s: %w", path, err)
	}
	if int64(len(body)) > limit {
		return nil, false, fmt.Errorf("file %s exceeds the secure size limit", path)
	}
	return body, true, nil
}

func dotenvToken(body []byte, key string) (string, bool, error) {
	for _, rawLine := range strings.Split(string(body), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, rawValue, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(name) != key {
			continue
		}
		value := strings.TrimSpace(rawValue)
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		}
		value, err := validateToken(strings.TrimSpace(value))
		if err != nil {
			return "", false, fmt.Errorf("invalid %s in Billyharness home dotenv: %w", key, err)
		}
		return value, true, nil
	}
	return "", false, nil
}

func validateToken(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("token is empty")
	}
	if len(value) > maxTokenBytes {
		return "", errors.New("token exceeds the secure size limit")
	}
	for index := 0; index < len(value); index++ {
		if value[index] < 0x21 || value[index] > 0x7e {
			return "", errors.New("token must contain only visible ASCII without whitespace")
		}
	}
	return value, nil
}

// ValidateToken accepts an explicit operator-provided bearer token without
// persisting it. It applies the same bounded visible-ASCII checks as the
// dedicated store.
func ValidateToken(value string) (string, error) {
	return validateToken(value)
}

func randomToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", err
	}
	return generatedPrefix + hex.EncodeToString(raw), nil
}

func ensurePrivateAuthDir(dir string) error {
	home := filepath.Dir(dir)
	homeExisted := true
	if _, err := os.Lstat(home); errors.Is(err, fs.ErrNotExist) {
		homeExisted = false
	} else if err != nil {
		return fmt.Errorf("inspect Billy home for gateway auth: %w", err)
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return fmt.Errorf("create Billy home for gateway auth: %w", err)
	}
	homeInfo, err := os.Lstat(home)
	if err != nil {
		return fmt.Errorf("inspect Billy home for gateway auth: %w", err)
	}
	if homeInfo.Mode()&os.ModeSymlink != 0 || !homeInfo.IsDir() {
		return fmt.Errorf("Billy home for gateway auth is not a real directory")
	}
	if err := validateDirectoryPlatform(homeInfo, home, false); err != nil {
		return err
	}
	if !homeExisted {
		if err := syncDirectory(filepath.Dir(home)); err != nil {
			return fmt.Errorf("sync Billy home parent for gateway auth: %w", err)
		}
	}

	dirCreated := false
	if err := os.Mkdir(dir, 0o700); err == nil {
		dirCreated = true
	} else if !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("create gateway auth directory %s: %w", dir, err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("inspect gateway auth directory %s: %w", dir, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("gateway auth directory %s is not a real directory", dir)
	}
	if err := validateDirectoryPlatform(info, dir, false); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("secure gateway auth directory %s: %w", dir, err)
	}
	secured, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("reinspect gateway auth directory %s: %w", dir, err)
	}
	if err := validateDirectoryPlatform(secured, dir, true); err != nil {
		return err
	}
	if err := syncDirectory(dir); err != nil {
		return fmt.Errorf("sync secured gateway auth directory: %w", err)
	}
	if dirCreated {
		if err := syncDirectory(home); err != nil {
			return fmt.Errorf("sync Billy home after creating gateway auth directory: %w", err)
		}
	}
	return nil
}

func publishTokenAtomic(path, value string) error {
	value, err := validateToken(value)
	if err != nil {
		return fmt.Errorf("refuse invalid gateway token: %w", err)
	}

	authDir := filepath.Dir(path)
	file, err := os.CreateTemp(authDir, ".gateway.token.tmp-")
	if err != nil {
		return fmt.Errorf("create temporary dedicated gateway token in %s: %w", authDir, err)
	}
	tempPath := file.Name()
	createdInfo, err := file.Stat()
	if err != nil {
		_ = file.Close()
		_ = os.Remove(tempPath)
		return fmt.Errorf("inspect temporary dedicated gateway token %s: %w", tempPath, err)
	}
	closed := false
	published := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
		if !published {
			removeCreatedToken(tempPath, createdInfo)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("secure temporary dedicated gateway token %s: %w", tempPath, err)
	}
	if err := validateOpenedRegular(file, tempPath, true); err != nil {
		return err
	}
	body := []byte(value + "\n")
	written, err := file.Write(body)
	if err != nil || written != len(body) {
		if err == nil {
			err = io.ErrShortWrite
		}
		return fmt.Errorf("persist temporary dedicated gateway token %s: %w", tempPath, err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync temporary dedicated gateway token %s: %w", tempPath, err)
	}
	if err := file.Close(); err != nil {
		closed = true
		return fmt.Errorf("close temporary dedicated gateway token %s: %w", tempPath, err)
	}
	closed = true

	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("dedicated gateway token %s appeared while publishing", path)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("inspect dedicated gateway token before publish %s: %w", path, err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("atomically publish dedicated gateway token %s: %w", path, err)
	}
	published = true
	if err := syncDirectory(authDir); err != nil {
		return fmt.Errorf("sync gateway auth directory: %w", err)
	}
	return nil
}

func removeCreatedToken(path string, expected fs.FileInfo) {
	current, err := os.Lstat(path)
	if err != nil || !os.SameFile(expected, current) {
		return
	}
	if err := os.Remove(path); err == nil {
		_ = syncDirectory(filepath.Dir(path))
	}
}

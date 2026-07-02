package attachments

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

const (
	DefaultMaxImageBytes  int64 = 20 << 20
	DefaultMaxImageWidth        = 8192
	DefaultMaxImageHeight       = 8192
	DefaultMaxImagePixels       = 40_000_000
)

type Store struct {
	Root           string
	MaxImageBytes  int64
	MaxImageWidth  int
	MaxImageHeight int
	MaxImagePixels int
}

type Resolved struct {
	Ref  protocol.AttachmentRef
	Path string
}

func DefaultStore() Store {
	return NewStore(DefaultStoreRoot())
}

func DefaultStoreRoot() string {
	return filepath.Join(config.BillyHomeDir(), "attachments")
}

func NewStore(root string) Store {
	return Store{
		Root:           root,
		MaxImageBytes:  DefaultMaxImageBytes,
		MaxImageWidth:  DefaultMaxImageWidth,
		MaxImageHeight: DefaultMaxImageHeight,
		MaxImagePixels: DefaultMaxImagePixels,
	}
}

func (s Store) ImportLocalImage(path string, detail protocol.AttachmentDetail) (protocol.AttachmentRef, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return protocol.AttachmentRef{}, errors.New("attachment path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return protocol.AttachmentRef{}, fmt.Errorf("resolve attachment path: %w", err)
	}
	if err := rejectSymlinkComponents(abs); err != nil {
		return protocol.AttachmentRef{}, err
	}
	stat, err := os.Lstat(abs)
	if err != nil {
		return protocol.AttachmentRef{}, fmt.Errorf("read attachment metadata: %w", err)
	}
	if !stat.Mode().IsRegular() {
		return protocol.AttachmentRef{}, fmt.Errorf("attachment %q is not a regular file", filepath.Base(abs))
	}
	if max := s.maxImageBytes(); max > 0 && stat.Size() > max {
		return protocol.AttachmentRef{}, fmt.Errorf("attachment %q is %d bytes; max is %d", filepath.Base(abs), stat.Size(), max)
	}
	file, err := os.Open(abs)
	if err != nil {
		return protocol.AttachmentRef{}, fmt.Errorf("open attachment: %w", err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return protocol.AttachmentRef{}, fmt.Errorf("stat opened attachment: %w", err)
	}
	if !os.SameFile(stat, opened) {
		return protocol.AttachmentRef{}, errors.New("attachment changed while opening; refusing unsafe path")
	}
	data, err := io.ReadAll(io.LimitReader(file, s.maxImageBytes()+1))
	if err != nil {
		return protocol.AttachmentRef{}, fmt.Errorf("read attachment: %w", err)
	}
	return s.StoreImageBytes(filepath.Base(abs), data, detail)
}

func (s Store) StoreImageBytes(fileName string, data []byte, detail protocol.AttachmentDetail) (protocol.AttachmentRef, error) {
	ref, ext, err := s.imageRef(fileName, data, detail)
	if err != nil {
		return protocol.AttachmentRef{}, err
	}
	ref.StorageRef = ref.ID + ext
	if err := s.ensureRoot(); err != nil {
		return protocol.AttachmentRef{}, err
	}
	path, err := s.storagePath(ref.StorageRef)
	if err != nil {
		return protocol.AttachmentRef{}, err
	}
	if err := writePrivateFile(path, data); err != nil {
		return protocol.AttachmentRef{}, err
	}
	return ref, nil
}

func (s Store) Resolve(ref protocol.AttachmentRef) (Resolved, error) {
	if strings.TrimSpace(ref.ID) == "" {
		return Resolved{}, errors.New("attachment id is required")
	}
	if strings.TrimSpace(ref.StorageRef) == "" {
		return Resolved{}, fmt.Errorf("attachment %s missing storage_ref", ref.ID)
	}
	path, err := s.storagePath(ref.StorageRef)
	if err != nil {
		return Resolved{}, err
	}
	stat, err := os.Lstat(path)
	if err != nil {
		return Resolved{}, fmt.Errorf("resolve attachment %s: %w", ref.ID, err)
	}
	if stat.Mode()&os.ModeSymlink != 0 {
		return Resolved{}, fmt.Errorf("attachment %s resolves to a symlink", ref.ID)
	}
	if !stat.Mode().IsRegular() {
		return Resolved{}, fmt.Errorf("attachment %s is not a regular file", ref.ID)
	}
	if ref.SizeBytes > 0 && stat.Size() != ref.SizeBytes {
		return Resolved{}, fmt.Errorf("attachment %s size changed", ref.ID)
	}
	if ref.SHA256 != "" {
		hash, err := fileSHA256(path)
		if err != nil {
			return Resolved{}, err
		}
		if !strings.EqualFold(hash, ref.SHA256) {
			return Resolved{}, fmt.Errorf("attachment %s hash changed", ref.ID)
		}
	}
	return Resolved{Ref: ref, Path: path}, nil
}

func (s Store) Read(ref protocol.AttachmentRef) ([]byte, protocol.AttachmentRef, error) {
	resolved, err := s.Resolve(ref)
	if err != nil {
		return nil, protocol.AttachmentRef{}, err
	}
	file, err := os.Open(resolved.Path)
	if err != nil {
		return nil, protocol.AttachmentRef{}, fmt.Errorf("open attachment %s: %w", ref.ID, err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, s.maxImageBytes()+1))
	if err != nil {
		return nil, protocol.AttachmentRef{}, fmt.Errorf("read attachment %s: %w", ref.ID, err)
	}
	if max := s.maxImageBytes(); max > 0 && int64(len(data)) > max {
		return nil, protocol.AttachmentRef{}, fmt.Errorf("attachment %s exceeds max size %d", ref.ID, max)
	}
	return data, resolved.Ref, nil
}

func (s Store) imageRef(fileName string, data []byte, detail protocol.AttachmentDetail) (protocol.AttachmentRef, string, error) {
	if max := s.maxImageBytes(); max > 0 && int64(len(data)) > max {
		return protocol.AttachmentRef{}, "", fmt.Errorf("attachment %q is %d bytes; max is %d", safeFileName(fileName), len(data), max)
	}
	mimeType, ext, err := sniffSupportedImage(data)
	if err != nil {
		return protocol.AttachmentRef{}, "", err
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return protocol.AttachmentRef{}, "", fmt.Errorf("decode image metadata: %w", err)
	}
	if err := s.validateDimensions(cfg.Width, cfg.Height); err != nil {
		return protocol.AttachmentRef{}, "", err
	}
	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])
	id := "att_" + sha[:24]
	return protocol.AttachmentRef{
		ID:        id,
		Kind:      protocol.AttachmentKindImage,
		FileName:  safeFileName(fileName),
		MIMEType:  mimeType,
		SizeBytes: int64(len(data)),
		Width:     cfg.Width,
		Height:    cfg.Height,
		SHA256:    sha,
		Detail:    detail,
	}, ext, nil
}

func (s Store) validateDimensions(width, height int) error {
	if width <= 0 || height <= 0 {
		return fmt.Errorf("image dimensions are invalid: %dx%d", width, height)
	}
	if max := s.maxImageWidth(); max > 0 && width > max {
		return fmt.Errorf("image width %d exceeds max %d", width, max)
	}
	if max := s.maxImageHeight(); max > 0 && height > max {
		return fmt.Errorf("image height %d exceeds max %d", height, max)
	}
	if max := s.maxImagePixels(); max > 0 && width*height > max {
		return fmt.Errorf("image pixels %d exceeds max %d", width*height, max)
	}
	return nil
}

func (s Store) ensureRoot() error {
	if strings.TrimSpace(s.Root) == "" {
		return errors.New("attachment store root is required")
	}
	if err := os.MkdirAll(s.Root, 0o700); err != nil {
		return fmt.Errorf("create attachment store: %w", err)
	}
	if err := os.Chmod(s.Root, 0o700); err != nil {
		return fmt.Errorf("set attachment store permissions: %w", err)
	}
	return nil
}

func (s Store) storagePath(storageRef string) (string, error) {
	if strings.TrimSpace(s.Root) == "" {
		return "", errors.New("attachment store root is required")
	}
	storageRef = filepath.Clean(strings.TrimSpace(storageRef))
	if storageRef == "." || filepath.IsAbs(storageRef) || strings.HasPrefix(storageRef, ".."+string(os.PathSeparator)) || storageRef == ".." {
		return "", fmt.Errorf("invalid attachment storage_ref %q", storageRef)
	}
	path := filepath.Join(s.Root, storageRef)
	rel, err := filepath.Rel(s.Root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("attachment storage_ref escapes store")
	}
	return path, nil
}

func (s Store) maxImageBytes() int64 {
	if s.MaxImageBytes > 0 {
		return s.MaxImageBytes
	}
	return DefaultMaxImageBytes
}

func (s Store) maxImageWidth() int {
	if s.MaxImageWidth > 0 {
		return s.MaxImageWidth
	}
	return DefaultMaxImageWidth
}

func (s Store) maxImageHeight() int {
	if s.MaxImageHeight > 0 {
		return s.MaxImageHeight
	}
	return DefaultMaxImageHeight
}

func (s Store) maxImagePixels() int {
	if s.MaxImagePixels > 0 {
		return s.MaxImagePixels
	}
	return DefaultMaxImagePixels
}

func sniffSupportedImage(data []byte) (string, string, error) {
	if len(data) == 0 {
		return "", "", errors.New("attachment is empty")
	}
	prefix := data
	if len(prefix) > 512 {
		prefix = prefix[:512]
	}
	switch mimeType := http.DetectContentType(prefix); mimeType {
	case "image/png":
		return mimeType, ".png", nil
	case "image/jpeg":
		return mimeType, ".jpg", nil
	case "image/gif":
		return mimeType, ".gif", nil
	default:
		return "", "", fmt.Errorf("unsupported image MIME type %q", mimeType)
	}
}

func writePrivateFile(path string, data []byte) error {
	if _, err := os.Stat(path); err == nil {
		if err := os.Chmod(path, 0o600); err != nil {
			return fmt.Errorf("set attachment permissions: %w", err)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat attachment destination: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-attachment-*")
	if err != nil {
		return fmt.Errorf("create attachment temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("set attachment temp permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write attachment temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close attachment temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("store attachment: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("set attachment permissions: %w", err)
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open attachment for hash: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("hash attachment: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func safeFileName(name string) string {
	name = strings.TrimSpace(filepath.Base(name))
	if name == "." || name == string(os.PathSeparator) || name == "" {
		return "image"
	}
	return name
}

func rejectSymlinkComponents(abs string) error {
	current := string(os.PathSeparator)
	rel := strings.TrimPrefix(filepath.Clean(abs), string(os.PathSeparator))
	for _, part := range strings.Split(rel, string(os.PathSeparator)) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return fmt.Errorf("read attachment path component: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("attachment path contains symlink component %q", part)
		}
	}
	return nil
}

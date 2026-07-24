package inventory

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	maxMetadataFile = 1 << 20
	maxEntries      = 512
	maxNesting      = 32
)

func canonicalDir(path string) (string, error) {
	if path == "" {
		return "", errors.New("empty directory")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("not a directory: %s", filepath.Base(path))
	}
	return filepath.Clean(resolved), nil
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func resolveInside(root, path string) (string, os.FileInfo, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", nil, err
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", nil, err
	}
	resolved = filepath.Clean(resolved)
	if !within(root, resolved) {
		return "", nil, errors.New("symlink escapes scan root")
	}
	info, err := os.Stat(resolved)
	return resolved, info, err
}

func readMetadata(root, path string) ([]byte, os.FileInfo, error) {
	resolved, info, err := resolveInside(root, path)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, errors.New("metadata source is not a regular file")
	}
	if info.Size() > maxMetadataFile {
		return nil, nil, fmt.Errorf("metadata source exceeds %d bytes", maxMetadataFile)
	}
	b, err := os.ReadFile(resolved)
	return b, info, err
}

func safeSource(path string) Source {
	clean := filepath.Clean(path)
	sum := sha256.Sum256([]byte(clean))
	return Source{Base: filepath.Base(clean), Hash: fmt.Sprintf("%x", sum[:8])}
}

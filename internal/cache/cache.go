package cache

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func dir() (string, error) {
	root := os.Getenv("IFTTT_CACHE_DIR")
	if root != "" {
		if err := os.MkdirAll(root, 0700); err != nil {
			return "", err
		}
		return root, nil
	}
	root, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	p := filepath.Join(root, "ifttt-lint")
	if err := os.MkdirAll(p, 0o700); err != nil {
		return "", err
	}
	return p, nil
}

func keyName(hash string) (string, error) {
	d, err := dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, fmt.Sprintf("local-%x.json.gz", sha256.Sum256([]byte(hash)))), nil
}

func Load(hash string, v any) error {
	p, err := keyName(hash)
	if err != nil {
		return err
	}
	f, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrNotFound
		}
		return err
	}
	defer f.Close()
	gr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gr.Close()
	return json.NewDecoder(io.LimitReader(gr, 32<<20)).Decode(v)
}

func Store(hash string, v any) error {
	p, err := keyName(hash)
	if err != nil {
		return err
	}
	dir := filepath.Dir(p)
	tmp, err := os.CreateTemp(dir, filepath.Base(p)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	gw := gzip.NewWriter(tmp)
	enc := json.NewEncoder(gw)
	if err := enc.Encode(v); err != nil {
		gw.Close()
		cleanup()
		return err
	}
	if err := gw.Close(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, p); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

var ErrNotFound = errors.New("cache: not found")

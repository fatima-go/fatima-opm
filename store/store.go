// Package store provides atomic, process-safe storage for the additive OPM API.
// Shared deployments require a shared POSIX filesystem supporting flock/rename.
package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

type Store struct{ Dir string }

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	return &Store{Dir: dir}, nil
}

// Update reloads under an interprocess lock. The callback must not perform RPCs.
func (s *Store) Update(name string, value any, fn func() error) error {
	lock, err := os.OpenFile(filepath.Join(s.Dir, name+".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	path := filepath.Join(s.Dir, name+".json")
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if len(b) > 0 {
		if err = json.Unmarshal(b, value); err != nil {
			return err
		}
	}
	if fn == nil {
		return nil
	}
	if err = fn(); err != nil {
		return err
	}
	before := b
	b, err = json.Marshal(value)
	if err != nil {
		return err
	}
	if bytes.Equal(before, b) {
		return nil
	}
	f, err := os.CreateTemp(s.Dir, ".state-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	d, err := os.Open(s.Dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

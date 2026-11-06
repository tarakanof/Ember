package producer

import (
	"fmt"
	"os"
	"path/filepath"
)

// A symlink at path is written through; a dangling one is replaced; an unwritable link target is an error.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	link := ""
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		if target, err := filepath.EvalSymlinks(path); err == nil {
			link, path = path, target
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		if link != "" {
			return fmt.Errorf("%s is a symlink to %s, which cannot be rewritten (%w); edit the target yourself or replace the link with a regular file", link, path, err)
		}
		return err
	}
	name := tmp.Name()
	fail := func(err error) error {
		tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Chmod(perm); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

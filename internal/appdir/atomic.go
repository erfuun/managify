package appdir

import (
	"io/fs"
	"os"
	"path/filepath"
)

// WriteFileAtomic writes data to a temp file next to path and renames it into
// place, so a crash never leaves a half-written config.
func WriteFileAtomic(path string, data []byte, perm fs.FileMode) (err error) {
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(tmp)
		}
	}()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Chmod(tmp, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

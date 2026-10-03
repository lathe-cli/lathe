package runtime

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

type binaryFile struct {
	path string
	tmp  *os.File
}

type binaryCause struct {
	cause error
}

func (e *binaryCause) Error() string { return "binary output failed" }

func (e *binaryCause) Unwrap() error { return e.cause }

func hideBinaryCause(err error) error {
	if err == nil {
		return nil
	}
	return &binaryCause{cause: err}
}

var errBinaryOutputExists = errors.New("output path exists")

func pathFreeErrno(err error) string {
	var errno syscall.Errno
	if errors.As(err, &errno) && errno != 0 {
		return errno.Error()
	}
	var pe *os.PathError
	if errors.As(err, &pe) && pe.Err != nil {
		return pe.Err.Error()
	}
	var le *os.LinkError
	if errors.As(err, &le) && le.Err != nil {
		return le.Err.Error()
	}
	return ""
}

func createBinaryFile(path string) (*binaryFile, error) {
	if _, err := os.Lstat(path); err == nil {
		return nil, errBinaryOutputExists
	} else if !os.IsNotExist(err) {
		return nil, hideBinaryCause(err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.part")
	if err != nil {
		return nil, hideBinaryCause(err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		name := tmp.Name()
		_ = tmp.Close()
		_ = os.Remove(name)
		return nil, hideBinaryCause(err)
	}
	return &binaryFile{path: path, tmp: tmp}, nil
}

func (f *binaryFile) abort() {
	if f == nil || f.tmp == nil {
		return
	}
	name := f.tmp.Name()
	_ = f.tmp.Close()
	_ = os.Remove(name)
	f.tmp = nil
}

func (f *binaryFile) commit() error {
	name := f.tmp.Name()
	if err := f.tmp.Close(); err != nil {
		_ = os.Remove(name)
		f.tmp = nil
		return hideBinaryCause(err)
	}
	f.tmp = nil
	if err := os.Link(name, f.path); err != nil {
		if !os.IsExist(err) {
			if _, statErr := os.Lstat(f.path); os.IsNotExist(statErr) {
				if renameErr := os.Rename(name, f.path); renameErr != nil {
					_ = os.Remove(name)
					return hideBinaryCause(renameErr)
				}
				return nil
			}
		}
		_ = os.Remove(name)
		if os.IsExist(err) {
			return errBinaryOutputExists
		}
		return hideBinaryCause(err)
	}
	_ = os.Remove(name)
	return nil
}

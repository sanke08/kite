package fs

import "os"

// Open opens path for reading and writing, creating the file if absent.
//
// TRAP: os.Create looks right and carries O_TRUNC — it would zero every page
// of an existing database on open.
func Open(path string) (File, error) {
	handle, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	return &osFile{handle: handle}, nil
}

// osFile is a File backed by an open file descriptor. The handle is a named
// field rather than embedded, so os.File's Seek and ~20 other methods do not
// become part of kite's API.
type osFile struct {
	handle *os.File
}

func (file *osFile) ReadAt(buf []byte, offset int64) (int, error) {
	return file.handle.ReadAt(buf, offset)
}

func (file *osFile) WriteAt(data []byte, offset int64) (int, error) {
	return file.handle.WriteAt(data, offset)
}

func (file *osFile) Sync() error { return file.handle.Sync() }

func (file *osFile) Truncate(size int64) error { return file.handle.Truncate(size) }

// Size stats the file rather than caching a length, which would go stale the
// moment anything else wrote to it.
func (file *osFile) Size() (int64, error) {
	info, err := file.handle.Stat()
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func (file *osFile) Close() error { return file.handle.Close() }

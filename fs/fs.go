// Package fs is the seam between kite and the operating system.
//
// Every byte that reaches disk passes through a File here. Nothing above this
// package touches an os.File directly, so a test can hand the engine a fake
// File that fails writes and crashes on demand.
package fs

// File is a random-access byte store: every read and write says which offset
// it wants.
//
// WHY: the alternative is a Seek-then-Read file that remembers a position.
// One File is shared by everything in the engine, so a remembered position is
// shared state — one caller seeking moves another caller's read, which then
// returns the wrong page and no error. An offset passed per call cannot be
// disturbed by anyone else.
type File interface {
	// ReadAt copies len(buf) bytes from the file into buf, starting at
	// offset in the file. buf always fills from index 0.
	//
	// WHY: io.ReaderAt semantics — if the file ends before buf is full this
	// errors instead of returning a short read. A partially-filled page
	// buffer still parses as a valid page, so a short read would corrupt
	// silently; an error stops it at the offset that is actually wrong.
	ReadAt(buf []byte, offset int64) (int, error)

	// WriteAt writes all of data at offset, extending the file if needed.
	//
	// WHY: returning nil means the bytes reached the OS, not the disk. Only
	// Sync promises the disk.
	WriteAt(data []byte, offset int64) (int, error)

	// Sync blocks until previously written bytes are durable.
	//
	// WHY: the commit point, and the reason this interface exists. The
	// journal protocol depends on ordering that only Sync provides:
	//
	//	write pre-images -> Sync -> overwrite pages -> Sync -> delete journal
	//
	// Without it the kernel flushes in whatever order it likes, and a crash
	// can leave the new pages with no journal to undo them.
	Sync() error

	// Truncate changes the file length to size.
	Truncate(size int64) error

	// Size reports the file length.
	//
	// WHY: the pager derives its page count from this. A zero-length file is
	// how "no pages yet" is detected, with no flag stored anywhere.
	Size() (int64, error)

	// Close releases the handle. It returns an error because some
	// filesystems only report a failed write when the file is closed.
	Close() error
}

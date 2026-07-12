package archive

import (
	"bufio"
	"os"
	"path/filepath"
	"time"
)

const DateFormat = "2006-01-02"

// Writer appends records to the current daily file. It is strictly
// append-only and only ever writes the current daily file (SPEC.md
// behavior 3); files are opened O_APPEND and never truncated.
type Writer struct {
	dir      string
	now      func() time.Time
	buffered bool

	f        *os.File
	bw       *bufio.Writer
	date     string
	repaired int64
}

func NewWriter(dir string, now func() time.Time, buffered bool) *Writer {
	return &Writer{dir: dir, now: now, buffered: buffered}
}

// Append writes line+'\n', opening or rotating the daily file as needed.
// rotated reports that a previous day's file was closed.
func (w *Writer) Append(line []byte) (rotated bool, err error) {
	today := w.today()
	if w.f != nil && w.date != today {
		if err := w.closeCurrent(); err != nil {
			return false, err
		}
		rotated = true
	}
	if w.f == nil {
		if err := w.open(today); err != nil {
			return rotated, err
		}
	}
	if w.bw != nil {
		if _, err := w.bw.Write(line); err != nil {
			return rotated, err
		}
		return rotated, w.bw.WriteByte('\n')
	}
	_, err = w.f.Write(append(line, '\n'))
	return rotated, err
}

// RotateIfDue closes the current file once its date has passed; the next
// Append lazily opens the new day's file. Needed so rotation also happens
// while no messages arrive.
func (w *Writer) RotateIfDue() (bool, error) {
	if w.f == nil || w.date == w.today() {
		return false, nil
	}
	return true, w.closeCurrent()
}

func (w *Writer) Flush() error {
	if w.bw == nil {
		return nil
	}
	return w.bw.Flush()
}

func (w *Writer) Sync() error {
	if w.f == nil {
		return nil
	}
	if err := w.Flush(); err != nil {
		return err
	}
	return w.f.Sync()
}

func (w *Writer) Close() error {
	if w.f == nil {
		return nil
	}
	return w.closeCurrent()
}

// Repaired counts crash-truncated lines terminated on open (SPEC.md
// behavior 8).
func (w *Writer) Repaired() int64 { return w.repaired }

func (w *Writer) today() string {
	return w.now().UTC().Format(DateFormat)
}

func (w *Writer) open(date string) error {
	path := filepath.Join(w.dir, date+".ndjson")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	repaired, err := repairTrailingNewline(f, path)
	if err != nil {
		_ = f.Close()
		return err
	}
	if repaired {
		w.repaired++
	}
	if err := syncDir(w.dir); err != nil {
		_ = f.Close()
		return err
	}
	w.f = f
	w.date = date
	if w.buffered {
		w.bw = bufio.NewWriter(f)
	}
	return nil
}

// repairTrailingNewline appends '\n' via the O_APPEND fd when the existing
// file ends mid-line — the partial line stays archived as its own line.
func repairTrailingNewline(f *os.File, path string) (bool, error) {
	fi, err := f.Stat()
	if err != nil {
		return false, err
	}
	if fi.Size() == 0 {
		return false, nil
	}
	r, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = r.Close() }()
	last := make([]byte, 1)
	if _, err := r.ReadAt(last, fi.Size()-1); err != nil {
		return false, err
	}
	if last[0] == '\n' {
		return false, nil
	}
	_, err = f.Write([]byte{'\n'})
	return err == nil, err
}

func (w *Writer) closeCurrent() error {
	err := w.Sync()
	if cerr := w.f.Close(); err == nil {
		err = cerr
	}
	w.f = nil
	w.bw = nil
	w.date = ""
	return err
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = d.Sync()
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	return err
}

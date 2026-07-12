package query

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"path/filepath"

	"github.com/klauspost/compress/zstd"
)

// maxLineBytes bounds a single archived line: the sink caps records at 16 MiB
// (docs/spec/archival.md) plus slack for anything hand-written into the file.
const maxLineBytes = 16<<20 + 64<<10

// lineReader yields only complete '\n'-terminated lines of one day and tracks
// the position of the next unread line — torn or partially flushed tails are
// never emitted and the position never advances past one.
type lineReader struct {
	closer io.Closer
	sc     *bufio.Scanner
	pos    Cursor
}

// openDayFrom opens c's day at c's position, preferring the plain file over
// an untrusted .zst per the sweep contract. It seeks with c.Off when the hint
// still fits the plain file and otherwise skips c.Line lines. A nil reader
// means the day has no file (a day without messages).
func openDayFrom(dir string, c Cursor) (*lineReader, error) {
	plainPath := filepath.Join(dir, c.Date+".ndjson")
	f, err := os.Open(plainPath)
	if err == nil {
		return plainFrom(f, c)
	}
	if !os.IsNotExist(err) {
		return nil, err
	}

	zf, err := os.Open(plainPath + ".zst")
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	dec, err := zstd.NewReader(zf)
	if err != nil {
		_ = zf.Close()
		return nil, err
	}
	r := newLineReader(zstCloser{dec, zf}, dec, Cursor{Date: c.Date})
	if err := r.skip(c.Line); err != nil {
		r.close()
		return nil, err
	}
	return r, nil
}

func plainFrom(f *os.File, c Cursor) (*lineReader, error) {
	if c.Off > 0 {
		fi, err := f.Stat()
		if err != nil {
			_ = f.Close()
			return nil, err
		}
		if fi.Size() >= c.Off {
			if _, err := f.Seek(c.Off, io.SeekStart); err != nil {
				_ = f.Close()
				return nil, err
			}
			return newLineReader(f, f, c), nil
		}
		// The offset hint outgrew the file on disk (it was taken from a plain
		// file that is gone); fall through and skip lines from the start.
	}
	r := newLineReader(f, f, Cursor{Date: c.Date})
	if err := r.skip(c.Line); err != nil {
		r.close()
		return nil, err
	}
	return r, nil
}

func newLineReader(closer io.Closer, rd io.Reader, pos Cursor) *lineReader {
	sc := bufio.NewScanner(rd)
	sc.Buffer(make([]byte, 64<<10), maxLineBytes)
	sc.Split(scanCompleteLines)
	return &lineReader{closer: closer, sc: sc, pos: pos}
}

// scanCompleteLines is bufio.ScanLines minus the final unterminated token:
// bytes after the last '\n' stay unread.
func scanCompleteLines(data []byte, _ bool) (int, []byte, error) {
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		return i + 1, data[:i], nil
	}
	return 0, nil, nil
}

// next returns the next complete line; ok=false at the end of complete data.
func (r *lineReader) next() (line []byte, ok bool, err error) {
	if !r.sc.Scan() {
		return nil, false, r.sc.Err()
	}
	line = r.sc.Bytes()
	r.pos.Line++
	r.pos.Off += int64(len(line)) + 1
	return line, true, nil
}

func (r *lineReader) skip(n int64) error {
	for i := int64(0); i < n; i++ {
		if _, ok, err := r.next(); err != nil || !ok {
			return err
		}
	}
	return nil
}

func (r *lineReader) close() { _ = r.closer.Close() }

// zstCloser releases the decoder and the underlying file together.
type zstCloser struct {
	dec *zstd.Decoder
	f   *os.File
}

func (z zstCloser) Close() error {
	z.dec.Close()
	return z.f.Close()
}

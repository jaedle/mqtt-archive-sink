package query

import (
	"bufio"
	"io"
	"os"
)

// WriteDay streams every complete line of the day to w as uncompressed
// NDJSON, decoding a .zst on the fly and withholding any torn trailing line.
// Returns os.ErrNotExist when the day has no file; nothing is written to w
// before the day file is open.
func WriteDay(dir, date string, w io.Writer) error {
	r, err := openDayFrom(dir, Cursor{Date: date})
	if err != nil {
		return err
	}
	if r == nil {
		return os.ErrNotExist
	}
	defer r.close()

	bw := bufio.NewWriter(w)
	for {
		line, ok, err := r.next()
		if err != nil {
			return err
		}
		if !ok {
			return bw.Flush()
		}
		if _, err := bw.Write(line); err != nil {
			return err
		}
		if err := bw.WriteByte('\n'); err != nil {
			return err
		}
	}
}

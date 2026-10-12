package table

import (
	"io"
	"os"

	"github.com/parquet-go/parquet-go"
)

// Reader reads a flat Parquet table row by row. Row values are indexed by the
// column positions Index returns; null values report IsNull.
type Reader struct {
	f    *os.File
	r    *parquet.Reader
	leaf map[string]int
	buf  []parquet.Row
	n, i int
}

func Open(path string) (*Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	r := parquet.NewReader(f)
	leaf := map[string]int{}
	for i, path := range r.Schema().Columns() {
		leaf[path[0]] = i
	}
	return &Reader{f: f, r: r, leaf: leaf, buf: make([]parquet.Row, 256)}, nil
}

// Index returns the position of a column in rows.
func (r *Reader) Index(name string) (int, bool) {
	i, ok := r.leaf[name]
	return i, ok
}

// Next returns the next row, valid until the following call, or io.EOF.
func (r *Reader) Next() (parquet.Row, error) {
	if r.i == r.n {
		n, err := r.r.ReadRows(r.buf)
		if n == 0 {
			if err == nil {
				err = io.EOF
			}
			return nil, err
		}
		r.n, r.i = n, 0
	}
	r.i++
	return r.buf[r.i-1], nil
}

func (r *Reader) Close() error {
	r.r.Close()
	return r.f.Close()
}

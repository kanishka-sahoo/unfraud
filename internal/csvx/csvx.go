// Package csvx reads large CSV files row by row with named columns.
package csvx

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"io"
	"os"
)

// Reader streams rows of a CSV file. Row slices are reused between calls.
type Reader struct {
	f    *os.File
	r    *csv.Reader
	cols map[string]int
	// Header is the header row, or nil when the file has none.
	Header []string
	line   int
}

// Open opens path. If hasHeader, the first row is read as the header.
func Open(path string, hasHeader bool) (*Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	r := csv.NewReader(bufio.NewReaderSize(f, 1<<20))
	r.ReuseRecord = true
	rd := &Reader{f: f, r: r}
	if hasHeader {
		h, err := r.Read()
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("%s: reading header: %w", path, err)
		}
		rd.Header = append([]string(nil), h...)
		rd.cols = make(map[string]int, len(h))
		for i, c := range rd.Header {
			rd.cols[c] = i
		}
		rd.line = 1
	}
	return rd, nil
}

// Read returns the next row, or io.EOF.
func (r *Reader) Read() ([]string, error) {
	row, err := r.r.Read()
	if err != nil {
		if err == io.EOF {
			return nil, err
		}
		return nil, fmt.Errorf("%s line %d: %w", r.f.Name(), r.line+1, err)
	}
	r.line++
	return row, nil
}

// Col returns the index of a header column, failing if it is missing.
func (r *Reader) Col(name string) (int, error) {
	i, ok := r.cols[name]
	if !ok {
		return 0, fmt.Errorf("%s: missing column %q", r.f.Name(), name)
	}
	return i, nil
}

// Line returns the line number of the last row read.
func (r *Reader) Line() int { return r.line }

func (r *Reader) Close() error { return r.f.Close() }

// Cols resolves several header columns at once.
func (r *Reader) Cols(names ...string) ([]int, error) {
	idx := make([]int, len(names))
	for i, n := range names {
		c, err := r.Col(n)
		if err != nil {
			return nil, err
		}
		idx[i] = c
	}
	return idx, nil
}

// Package table writes flat Parquet tables with columns known at runtime.
package table

import (
	"fmt"
	"os"

	"github.com/parquet-go/parquet-go"
)

// Type is a column's physical type. All columns are optional (nullable).
type Type int

const (
	Float32 Type = iota
	Int64
	String
)

type Column struct {
	Name string
	Type Type
}

// Writer writes rows one at a time: set each column, then call WriteRow.
// Columns left unset in a row are written as null.
type Writer struct {
	f     *os.File
	w     *parquet.Writer
	index []int // our column -> parquet leaf index
	vals  []parquet.Value
	row   parquet.Row
	set   []bool
}

func Create(path string, cols []Column) (*Writer, error) {
	g := parquet.Group{}
	for _, c := range cols {
		var n parquet.Node
		switch c.Type {
		case Float32:
			n = parquet.Leaf(parquet.FloatType)
		case Int64:
			n = parquet.Int(64)
		case String:
			n = parquet.String()
		}
		if _, dup := g[c.Name]; dup {
			return nil, fmt.Errorf("table: duplicate column %q", c.Name)
		}
		g[c.Name] = parquet.Optional(n)
	}
	schema := parquet.NewSchema("table", g)
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	w := &Writer{
		f:     f,
		w:     parquet.NewWriter(f, schema, parquet.Compression(&parquet.Zstd), parquet.MaxRowsPerRowGroup(8*1024)),
		index: make([]int, len(cols)),
		vals:  make([]parquet.Value, len(cols)),
		row:   make(parquet.Row, len(cols)),
		set:   make([]bool, len(cols)),
	}
	for i, c := range cols {
		leaf, _ := schema.Lookup(c.Name)
		w.index[i] = leaf.ColumnIndex
	}
	return w, nil
}

func (w *Writer) SetFloat32(col int, v float32) {
	w.vals[col], w.set[col] = parquet.FloatValue(v), true
}
func (w *Writer) SetInt64(col int, v int64) { w.vals[col], w.set[col] = parquet.Int64Value(v), true }
func (w *Writer) SetString(col int, v string) {
	w.vals[col], w.set[col] = parquet.ByteArrayValue([]byte(v)), true
}

// WriteRow writes the current values and clears them.
func (w *Writer) WriteRow() error {
	for i, idx := range w.index {
		if w.set[i] {
			w.row[idx] = w.vals[i].Level(0, 1, idx)
		} else {
			w.row[idx] = parquet.NullValue().Level(0, 0, idx)
		}
		w.set[i] = false
	}
	_, err := w.w.WriteRows([]parquet.Row{w.row})
	return err
}

func (w *Writer) Close() error {
	if err := w.w.Close(); err != nil {
		w.f.Close()
		return err
	}
	return w.f.Close()
}

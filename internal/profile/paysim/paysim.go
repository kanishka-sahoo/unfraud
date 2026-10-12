// Package paysim adapts the PaySim synthetic mobile-money dataset. It is used
// for load testing only (ADR-0001): the balance columns give away the label
// and are dropped, and origin accounts almost never repeat.
package paysim

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	unfraudv1 "github.com/kanishka-sahoo/unfraud/gen/unfraud/v1"
	"github.com/kanishka-sahoo/unfraud/internal/csvx"
	"github.com/kanishka-sahoo/unfraud/internal/profile"
)

const name = "paysim"

// base is the Event Time of step 0. PaySim steps are hours with no calendar
// date, so the base is an arbitrary, documented convention.
var base = time.Date(2017, 1, 1, 0, 0, 0, 0, time.UTC)

var schema = profile.NewSchema([]string{"amount"}, []string{"type"})

var Profile = profile.Profile{
	Name:              name,
	LabelDelay:        7 * 24 * time.Hour,
	WatermarkInterval: time.Hour,
	Schema:            schema,
	Open:              open,
}

const file = "PS_20174392719_1491204439457_log.csv"

type source struct {
	r                                           *csvx.Reader
	step, typ, amount, orig, dest, isFraud, row int
}

func open(dir string) (profile.Source, error) {
	r, err := csvx.Open(filepath.Join(dir, file), true)
	if err != nil {
		return nil, err
	}
	c, err := r.Cols("step", "type", "amount", "nameOrig", "nameDest", "isFraud")
	if err != nil {
		r.Close()
		return nil, err
	}
	return &source{r: r, step: c[0], typ: c[1], amount: c[2], orig: c[3], dest: c[4], isFraud: c[5]}, nil
}

func (s *source) Next() (profile.Record, error) {
	row, err := s.r.Read()
	if err != nil {
		return profile.Record{}, err
	}
	s.row++
	step, err := strconv.Atoi(row[s.step])
	if err != nil {
		return profile.Record{}, fmt.Errorf("paysim line %d: step: %w", s.r.Line(), err)
	}
	amount, err := strconv.ParseFloat(row[s.amount], 64)
	if err != nil {
		return profile.Record{}, fmt.Errorf("paysim line %d: amount: %w", s.r.Line(), err)
	}
	label := unfraudv1.Label_LABEL_LEGIT
	if row[s.isFraud] == "1" {
		label = unfraudv1.Label_LABEL_FRAUD
	}
	orig := row[s.orig]
	ev := &unfraudv1.Event{
		EventId:  strconv.Itoa(s.row),
		Entities: map[string]string{"orig": orig, "dest": row[s.dest]},
		SchemaId: schema.ID,
		Num:      []float64{amount},
		Cat:      []string{strings.Clone(row[s.typ])},
	}
	t := base.Add(time.Duration(step) * time.Hour).UnixMilli()
	return profile.Record{Env: profile.NewEvent(name, t, ev), Key: orig, Label: label}, nil
}

func (s *source) Close() error { return s.r.Close() }

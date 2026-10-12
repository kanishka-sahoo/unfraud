// Package ieeecis adapts the IEEE-CIS (Vesta) fraud detection training set,
// the main Profile for fraud metrics (ADR-0001).
package ieeecis

import (
	"fmt"
	"io"
	"math"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	unfraudv1 "github.com/kanishka-sahoo/unfraud/gen/unfraud/v1"
	"github.com/kanishka-sahoo/unfraud/internal/csvx"
	"github.com/kanishka-sahoo/unfraud/internal/profile"
)

const name = "ieee-cis"

// base is the reference for TransactionDT, which counts seconds from an
// undisclosed start. 2017-11-30 is the date the Kaggle community converged on.
var base = time.Date(2017, 11, 30, 0, 0, 0, 0, time.UTC)

// Entity types.
const (
	CardUID = "card_uid"
	Device  = "device"
	EmailP  = "email_p"
	EmailR  = "email_r"
	Addr    = "addr"
)

func seq(prefix string, from, to int, format string) []string {
	var n []string
	for i := from; i <= to; i++ {
		n = append(n, prefix+fmt.Sprintf(format, i))
	}
	return n
}

func concat(parts ...[]string) []string {
	var n []string
	for _, p := range parts {
		n = append(n, p...)
	}
	return n
}

// schema follows Kaggle's documentation of which columns are categorical.
// card1-3, card5 and addr1-2 are categorical codes but numeric-valued, so
// they stay numeric for the model.
var schema = profile.NewSchema(
	concat(
		[]string{"TransactionAmt", "card1", "card2", "card3", "card5", "addr1", "addr2", "dist1", "dist2"},
		seq("C", 1, 14, "%d"), seq("D", 1, 15, "%d"), seq("V", 1, 339, "%d"), seq("id_", 1, 11, "%02d"),
	),
	concat(
		[]string{"ProductCD", "card4", "card6", "P_emaildomain", "R_emaildomain"},
		seq("M", 1, 9, "%d"), seq("id_", 12, 38, "%02d"), []string{"DeviceType", "DeviceInfo"},
	),
)

var Profile = profile.Profile{
	Name:              name,
	LabelDelay:        30 * 24 * time.Hour,
	WatermarkInterval: time.Hour,
	Schema:            schema,
	Features:          features,
	Rules:             rules,
	// Kaggle documents only these columns; V, C, D, M and id_* are masked.
	Glossary: map[string]string{
		"TransactionAmt": "transaction amount in USD",
		"ProductCD":      "product code of the purchase",
		"card4":          "card network",
		"card6":          "card type (credit or debit)",
		"P_emaildomain":  "purchaser email domain",
		"R_emaildomain":  "recipient email domain",
		"addr1":          "billing region code",
		"addr2":          "billing country code",
		"dist1":          "a distance between addresses (units undisclosed)",
		"DeviceType":     "device type (mobile or desktop)",
		"DeviceInfo":     "device model or operating system string",
	},
	// Chosen on the training months only: devices shared by 2-20 cards carry
	// 9-14% fraud against 3.5% overall, while email domains and addresses are
	// almost all Super-nodes and are not used for links.
	Graph: &profile.GraphConfig{
		Node: CardUID, Links: []string{Device}, MaxDegree: 20,
		Window: 30 * 24 * time.Hour, Rebuild: 24 * time.Hour, FraudRetention: 90 * 24 * time.Hour,
		// Ring Alerts are off: on the validation month the best policy found
		// (>=3 cards, >=2 with released fraud) flagged 3,880 model-approved
		// Events at 5.1% precision, against 18.9% for the model's next 3,880.
		// The graph's signal reaches the model through its graph features.
		RingMinNodes: 0,
	},
	Open: open,
}

// Schema positions used to derive Entities.
var (
	card1   = mustNum("card1")
	addr1   = mustNum("addr1")
	addr2   = mustNum("addr2")
	d1      = mustNum("D1")
	emailP  = mustCat("P_emaildomain")
	emailR  = mustCat("R_emaildomain")
	devCols = []int{mustCat("DeviceInfo"), mustCat("id_30"), mustCat("id_31"), mustCat("id_33")}
)

func mustNum(n string) int {
	i, ok := schema.NumIndex(n)
	if !ok {
		panic(n)
	}
	return i
}

func mustCat(n string) int {
	i, ok := schema.CatIndex(n)
	if !ok {
		panic(n)
	}
	return i
}

// slot says where a CSV column goes: a numeric (num) or categorical (!num)
// schema position, or nowhere (skip).
type slot struct {
	skip, num bool
	idx       int
}

// slots maps a file's header onto the schema. Columns outside the schema are
// an error, so a changed dataset fails loudly instead of losing features.
func slots(header []string, skip ...string) ([]slot, error) {
	out := make([]slot, len(header))
	for i, col := range header {
		if slices.Contains(skip, col) {
			out[i] = slot{skip: true}
		} else if j, ok := schema.NumIndex(col); ok {
			out[i] = slot{num: true, idx: j}
		} else if j, ok := schema.CatIndex(col); ok {
			out[i] = slot{idx: j}
		} else {
			return nil, fmt.Errorf("column %q is not in the ieee-cis schema", col)
		}
	}
	return out, nil
}

type source struct {
	tx       *csvx.Reader
	txSlots  []slot
	identity map[string][]string
	idSlots  []slot
	txID     int
	isFraud  int
	dt       int
}

func open(dir string) (profile.Source, error) {
	identity, idSlots, err := readIdentity(filepath.Join(dir, "train_identity.csv"))
	if err != nil {
		return nil, err
	}
	tx, err := csvx.Open(filepath.Join(dir, "train_transaction.csv"), true)
	if err != nil {
		return nil, err
	}
	c, err := tx.Cols("TransactionID", "isFraud", "TransactionDT")
	if err != nil {
		tx.Close()
		return nil, err
	}
	txSlots, err := slots(tx.Header, "TransactionID", "isFraud", "TransactionDT")
	if err != nil {
		tx.Close()
		return nil, fmt.Errorf("train_transaction.csv: %w", err)
	}
	return &source{tx: tx, txSlots: txSlots, identity: identity, idSlots: idSlots, txID: c[0], isFraud: c[1], dt: c[2]}, nil
}

func readIdentity(path string) (map[string][]string, []slot, error) {
	r, err := csvx.Open(path, true)
	if err != nil {
		return nil, nil, err
	}
	defer r.Close()
	id, err := r.Col("TransactionID")
	if err != nil {
		return nil, nil, err
	}
	sl, err := slots(r.Header, "TransactionID")
	if err != nil {
		return nil, nil, fmt.Errorf("train_identity.csv: %w", err)
	}
	m := make(map[string][]string, 150_000)
	for {
		row, err := r.Read()
		if err == io.EOF {
			return m, sl, nil
		}
		if err != nil {
			return nil, nil, err
		}
		m[row[id]] = append([]string(nil), row...)
	}
}

func (s *source) Next() (profile.Record, error) {
	row, err := s.tx.Read()
	if err != nil {
		return profile.Record{}, err
	}
	line := s.tx.Line()
	dt, err := strconv.ParseInt(row[s.dt], 10, 64)
	if err != nil {
		return profile.Record{}, fmt.Errorf("ieee-cis line %d: TransactionDT: %w", line, err)
	}
	ev := &unfraudv1.Event{
		EventId:  row[s.txID],
		Entities: make(map[string]string, 5),
		SchemaId: schema.ID,
		Num:      schema.NewNum(),
		Cat:      schema.NewCat(),
	}
	if err := fill(ev, s.txSlots, row); err != nil {
		return profile.Record{}, fmt.Errorf("ieee-cis line %d: %w", line, err)
	}
	if idRow, ok := s.identity[ev.EventId]; ok {
		if err := fill(ev, s.idSlots, idRow); err != nil {
			return profile.Record{}, fmt.Errorf("ieee-cis identity %s: %w", ev.EventId, err)
		}
	}
	setEntities(ev, dt)

	label := unfraudv1.Label_LABEL_LEGIT
	if row[s.isFraud] == "1" {
		label = unfraudv1.Label_LABEL_FRAUD
	}
	t := base.Add(time.Duration(dt) * time.Second).UnixMilli()
	return profile.Record{Env: profile.NewEvent(name, t, ev), Key: ev.Entities[CardUID], Label: label}, nil
}

func fill(ev *unfraudv1.Event, sl []slot, row []string) error {
	for i, v := range row {
		if v == "" || sl[i].skip {
			continue
		}
		if !sl[i].num {
			ev.Cat[sl[i].idx] = v
			continue
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return fmt.Errorf("%s: %w", schema.Num[sl[i].idx], err)
		}
		ev.Num[sl[i].idx] = f
	}
	return nil
}

// setEntities derives the Entities. The Card UID follows the community recipe
// card1 + addr1 + (transaction day - D1), which approximates the day the card
// was first seen; missing parts become "nan" as in that recipe.
func setEntities(ev *unfraudv1.Event, dt int64) {
	day := "nan"
	if v := ev.Num[d1]; !math.IsNaN(v) {
		day = strconv.FormatFloat(math.Floor(float64(dt)/86400-v), 'f', -1, 64)
	}
	ev.Entities[CardUID] = numStr(ev, card1) + "_" + numStr(ev, addr1) + "_" + day

	parts := make([]string, len(devCols))
	found := false
	for i, c := range devCols {
		parts[i] = ev.Cat[c]
		found = found || parts[i] != ""
	}
	if found {
		ev.Entities[Device] = strings.Join(parts, "|")
	}
	if v := ev.Cat[emailP]; v != "" {
		ev.Entities[EmailP] = v
	}
	if v := ev.Cat[emailR]; v != "" {
		ev.Entities[EmailR] = v
	}
	if !math.IsNaN(ev.Num[addr1]) {
		ev.Entities[Addr] = numStr(ev, addr1) + "_" + numStr(ev, addr2)
	}
}

func numStr(ev *unfraudv1.Event, i int) string {
	v := ev.Num[i]
	if math.IsNaN(v) {
		return "nan"
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func (s *source) Close() error { return s.tx.Close() }

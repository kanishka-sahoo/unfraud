package features

import "context"

// Memory is an in-process State, used by Accuracy Runs and tests.
type Memory struct {
	series map[string]*series
	sets   map[string]*set
	lasts  map[string]*last
}

type bucket struct {
	idx int64
	val float64
}

type series struct {
	buckets []bucket // ascending idx
	expire  int64
}

type set struct {
	seen   map[string]int64
	retain int64 // how long a member stays visible after it was last seen
	expire int64
}

type last struct {
	time   int64
	str    string
	expire int64
}

func NewMemory() *Memory {
	return &Memory{series: map[string]*series{}, sets: map[string]*set{}, lasts: map[string]*last{}}
}

func (m *Memory) Get(_ context.Context, qs []Query, out []Result) error {
	for i, q := range qs {
		var r Result
		switch q.Kind {
		case QSum:
			if s := m.series[q.Key]; s != nil {
				for _, b := range s.buckets {
					if b.idx >= q.From && b.idx <= q.To {
						r.Num += b.val
					}
				}
			}
			r.OK = true
		case QCountSince:
			if s := m.sets[q.Key]; s != nil {
				for _, t := range s.seen {
					if t >= q.From {
						r.Num++
					}
				}
			}
			r.OK = true
		case QMember:
			if s := m.sets[q.Key]; s != nil {
				r.Time, r.OK = s.seen[q.Member]
			}
		case QLast:
			if l := m.lasts[q.Key]; l != nil {
				r = Result{Time: l.time, Str: l.str, OK: true}
			}
		}
		out[i] = r
	}
	return nil
}

func (m *Memory) Apply(_ context.Context, us []Update) error {
	for _, u := range us {
		switch u.Kind {
		case UIncr:
			s := m.series[u.Key]
			if s == nil {
				s = &series{}
				m.series[u.Key] = s
			}
			if n := len(s.buckets); n > 0 && s.buckets[n-1].idx == u.Bucket {
				s.buckets[n-1].val += u.Delta
			} else {
				s.buckets = append(s.buckets, bucket{u.Bucket, u.Delta})
			}
			drop := 0
			for drop < len(s.buckets) && s.buckets[drop].idx <= u.Bucket-u.Keep {
				drop++
			}
			s.buckets = s.buckets[drop:]
			s.expire = max(s.expire, u.ExpireMs)
		case UTouch:
			s := m.sets[u.Key]
			if s == nil {
				s = &set{seen: map[string]int64{}}
				m.sets[u.Key] = s
			}
			s.seen[u.Member] = u.Time
			s.retain = u.ExpireMs - u.Time
			s.expire = max(s.expire, u.ExpireMs)
		case USetLast:
			m.lasts[u.Key] = &last{time: u.Time, str: u.Str, expire: u.ExpireMs}
		}
	}
	return nil
}

// Expire also forgets set members no query can see any more, which keeps
// Super-node sets (shared devices, popular addresses) from growing forever.
func (m *Memory) Expire(_ context.Context, now int64) error {
	for k, s := range m.series {
		if s.expire < now {
			delete(m.series, k)
		}
	}
	for k, s := range m.sets {
		if s.expire < now {
			delete(m.sets, k)
			continue
		}
		for mem, t := range s.seen {
			if t+s.retain < now {
				delete(s.seen, mem)
			}
		}
	}
	for k, l := range m.lasts {
		if l.expire < now {
			delete(m.lasts, k)
		}
	}
	return nil
}

// Size reports how many keys are held, for memory diagnostics.
func (m *Memory) Size() (series, sets, lasts int) {
	return len(m.series), len(m.sets), len(m.lasts)
}

package features

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/valkey-io/valkey-go"
)

// Valkey is a State shared by every scorer replica. Its semantics match
// Memory exactly, so live features equal Accuracy Run features:
//
//   - bucketed series are hashes (bucket -> value), updated in Lua with
//     double arithmetic and written back with 17 significant digits, so sums
//     are bit-identical to Go float64 (HINCRBYFLOAT uses long double);
//   - sets are sorted sets scored by last-seen time, so distinct counts are
//     exact sliding windows (ZCOUNT);
//   - expiry is in Event Time, through an index sorted set, because
//     wall-clock TTLs are wrong when replays run faster than real time.
type Valkey struct {
	c      valkey.Client
	prefix string
	expiry string
}

// NewValkey uses keys under prefix (for example "ieee-cis:") so several
// Profiles or runs can share one server.
func NewValkey(c valkey.Client, prefix string) *Valkey {
	return &Valkey{c: c, prefix: prefix, expiry: prefix + "expiry"}
}

// Exchange pipelines the reads and the update script on one connection, so
// Valkey runs them in order: one round trip per Event.
func (v *Valkey) Exchange(ctx context.Context, g Guard, qs []Query, out []Result, us []Update) error {
	cmds := make(valkey.Commands, len(qs), len(qs)+1)
	for i, q := range qs {
		k := v.prefix + q.Key
		switch q.Kind {
		case QSum:
			fields := make([]string, 0, q.To-q.From+1)
			for b := q.From; b <= q.To; b++ {
				fields = append(fields, strconv.FormatInt(b, 10))
			}
			cmds[i] = v.c.B().Hmget().Key(k).Field(fields...).Build()
		case QCountSince:
			cmds[i] = v.c.B().Zcount().Key(k).Min(strconv.FormatInt(q.From, 10)).Max("+inf").Build()
		case QMember:
			cmds[i] = v.c.B().Zscore().Key(k).Member(q.Member).Build()
		case QLast:
			cmds[i] = v.c.B().Hmget().Key(k).Field("t", "s").Build()
		default:
			return fmt.Errorf("valkey state: unknown query kind %d", q.Kind)
		}
	}
	keys, args, err := v.applyArgs(g, us)
	if err != nil {
		return err
	}
	if len(us) > 0 {
		cmds = append(cmds, v.c.B().Evalsha().Sha1(applySHA).Numkeys(int64(len(keys))).Key(keys...).Arg(args...).Build())
	}
	resps := v.c.DoMulti(ctx, cmds...)
	for i := range qs {
		r, err := decode(qs[i].Kind, resps[i])
		if err != nil {
			return fmt.Errorf("valkey state: %s: %w", qs[i].Key, err)
		}
		out[i] = r
	}
	if len(us) == 0 {
		return nil
	}
	err = resps[len(qs)].Error()
	if err != nil && strings.HasPrefix(err.Error(), "NOSCRIPT") {
		// The script isn't cached yet (first call, or after a restart) and
		// did not run, so running it now applies the updates exactly once.
		return applyScript.Exec(ctx, v.c, keys, args).Error()
	}
	return err
}

func decode(kind QueryKind, resp valkey.ValkeyResult) (Result, error) {
	var r Result
	switch kind {
	case QSum:
		vals, err := resp.ToArray()
		if err != nil {
			return r, err
		}
		for _, val := range vals {
			s, err := val.ToString()
			if valkey.IsValkeyNil(err) {
				continue
			}
			if err != nil {
				return r, err
			}
			f, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return r, err
			}
			r.Num += f
		}
		r.OK = true
	case QCountSince:
		n, err := resp.AsInt64()
		if err != nil {
			return r, err
		}
		r.Num, r.OK = float64(n), true
	case QMember:
		f, err := resp.AsFloat64()
		if valkey.IsValkeyNil(err) {
			return r, nil
		}
		if err != nil {
			return r, err
		}
		r.Time, r.OK = int64(f), true
	case QLast:
		vals, err := resp.ToArray()
		if err != nil {
			return r, err
		}
		t, err := vals[0].ToString()
		if valkey.IsValkeyNil(err) {
			return r, nil
		}
		if err != nil {
			return r, err
		}
		if r.Time, err = strconv.ParseInt(t, 10, 64); err != nil {
			return r, err
		}
		r.Str, _ = vals[1].ToString()
		r.OK = true
	}
	return r, nil
}

var applySHA = func() string {
	h := sha1.Sum([]byte(applySource))
	return hex.EncodeToString(h[:])
}()

// applyScript runs a batch of Updates atomically. KEYS[1] is the expiry
// index and KEYS[2] the Guard key ("" for none, expiring at ARGV[1]); if the
// Guard is already set the batch was applied before and nothing changes.
// KEYS[i+2] belongs to the i-th update, whose arguments start at
// ARGV[6*(i-1)+2]: kind, bucket|time, keep|retain, delta|member, str, expire.
var applyScript = valkey.NewLuaScript(applySource)

const applySource = `
local exp = KEYS[1]
if KEYS[2] ~= "" then
  if not redis.call("SET", KEYS[2], "1", "NX") then
    return -1
  end
  redis.call("ZADD", exp, ARGV[1], KEYS[2])
end
for i = 3, #KEYS do
  local key = KEYS[i]
  local a = 6 * (i - 3) + 1
  local kind = ARGV[a + 1]
  if kind == "1" then
    local b = ARGV[a + 2]
    local cur = tonumber(redis.call("HGET", key, b) or "0")
    redis.call("HSET", key, b, string.format("%.17g", cur + tonumber(ARGV[a + 4])))
    redis.call("HDEL", key, string.format("%d", tonumber(b) - tonumber(ARGV[a + 3])))
  elseif kind == "2" then
    redis.call("ZADD", key, ARGV[a + 2], ARGV[a + 4])
    local oldest = tonumber(ARGV[a + 2]) - tonumber(ARGV[a + 3])
    redis.call("ZREMRANGEBYSCORE", key, "-inf", "(" .. string.format("%.17g", oldest))
  else
    redis.call("HSET", key, "t", ARGV[a + 2], "s", ARGV[a + 5])
  end
  redis.call("ZADD", exp, "GT", ARGV[a + 6], key)
end
return #KEYS - 2
`

func (v *Valkey) Apply(ctx context.Context, g Guard, us []Update) error {
	if len(us) == 0 {
		return nil
	}
	keys, args, err := v.applyArgs(g, us)
	if err != nil {
		return err
	}
	return applyScript.Exec(ctx, v.c, keys, args).Error()
}

func (v *Valkey) applyArgs(g Guard, us []Update) ([]string, []string, error) {
	keys := make([]string, 2, len(us)+2)
	keys[0], keys[1] = v.expiry, v.prefix+"g|"+g.Key
	args := make([]string, 1, 6*len(us)+1)
	args[0] = strconv.FormatInt(g.ExpireMs, 10)
	if g.Key == "" {
		keys[1], args[0] = "", ""
	}
	for _, u := range us {
		keys = append(keys, v.prefix+u.Key)
		exp := strconv.FormatInt(u.ExpireMs, 10)
		switch u.Kind {
		case UIncr:
			args = append(args, "1", strconv.FormatInt(u.Bucket, 10), strconv.FormatInt(u.Keep, 10),
				strconv.FormatFloat(u.Delta, 'g', -1, 64), "", exp)
		case UTouch:
			args = append(args, "2", strconv.FormatInt(u.Time, 10), strconv.FormatInt(u.ExpireMs-u.Time, 10),
				u.Member, "", exp)
		case USetLast:
			args = append(args, "3", strconv.FormatInt(u.Time, 10), "", "", u.Str, exp)
		default:
			return nil, nil, fmt.Errorf("valkey state: unknown update kind %d", u.Kind)
		}
	}
	return keys, args, nil
}

// expireScript deletes up to ARGV[2] keys whose expiry is before ARGV[1].
var expireScript = valkey.NewLuaScript(`
local keys = redis.call("ZRANGEBYSCORE", KEYS[1], "-inf", "(" .. ARGV[1], "LIMIT", 0, tonumber(ARGV[2]))
for _, k in ipairs(keys) do
  redis.call("DEL", k)
end
if #keys > 0 then
  redis.call("ZREM", KEYS[1], unpack(keys))
end
return #keys
`)

func (v *Valkey) Expire(ctx context.Context, now int64) error {
	const batch = 1000
	for {
		n, err := expireScript.Exec(ctx, v.c, []string{v.expiry}, []string{strconv.FormatInt(now, 10), strconv.Itoa(batch)}).AsInt64()
		if err != nil {
			return fmt.Errorf("valkey state: expire: %w", err)
		}
		if n < batch {
			return nil
		}
	}
}

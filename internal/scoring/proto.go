package scoring

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"time"

	unfraudv1 "github.com/kanishka-sahoo/unfraud/gen/unfraud/v1"
	"github.com/kanishka-sahoo/unfraud/internal/profile"
)

var actions = map[profile.Action]unfraudv1.Action{
	profile.Approve: unfraudv1.Action_ACTION_APPROVE,
	profile.Review:  unfraudv1.Action_ACTION_REVIEW,
	profile.Block:   unfraudv1.Action_ACTION_BLOCK,
}

// Proto converts a Decision for the scored.<profile> and revised.<profile>
// topics. The model input is kept only for review and block Decisions, for
// Contributions downstream.
func Proto(prof, version string, stage unfraudv1.Stage, d *Decision, ingested, now time.Time) *unfraudv1.Scored {
	out := &unfraudv1.Scored{
		Profile: prof, EventId: d.EventID, EventTimeMs: d.EventTimeMs, Entities: d.Entities,
		Probability: d.Probability, RawScore: d.Raw,
		ModelAction: actions[d.ModelAction], RuleAction: actions[d.RuleAction], Action: actions[d.Action],
		ModelVersion: version, IngestedAtMs: ingested.UnixMilli(), ScoredAtMs: now.UnixMilli(), Stage: stage,
	}
	for _, h := range d.Hits {
		out.ReasonCodes = append(out.ReasonCodes, h.Code)
	}
	if d.Action != profile.Approve {
		out.ModelInput = d.Input
	}
	return out
}

// ModelVersion is a short hash of a model folder's model.txt, so Decisions
// name the exact artifact that produced them.
func ModelVersion(dir string) (string, error) {
	f, err := os.Open(filepath.Join(dir, "model.txt"))
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil))[:12], nil
}

package agent

import (
	"context"
	"encoding/json"
	"hash/fnv"
	"log/slog"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

const (
	ArmControl   = "control"
	ArmCandidate = "candidate"
)

// experimentArm identifies the experiment and arm a run belongs to. The zero
// value means "not in an experiment".
type experimentArm struct{ ExperimentID, Arm string }

// experimentBucket maps (experiment, task) deterministically onto 0..99. The
// experiment id is mixed in so successive experiments don't always pick the
// same tasks.
func experimentBucket(experimentID, taskID string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(experimentID + ":" + taskID))
	return int(h.Sum32() % 100)
}

// assignArm returns the arm for a task given the candidate share in percent.
func assignArm(experimentID, taskID string, share int64) string {
	if int64(experimentBucket(experimentID, taskID)) < share {
		return ArmCandidate
	}
	return ArmControl
}

// loadRunningExperiments returns running experiments keyed by agent config id.
// Failures degrade to "no experiments" so dispatch is never blocked.
func (d *Dispatcher) loadRunningExperiments(ctx context.Context) map[string]*gen.AgentExperiment {
	out := map[string]*gen.AgentExperiment{}
	exps, err := d.q.ListRunningAgentExperiments(ctx)
	if err != nil {
		slog.Warn("dispatcher: list running experiments", "component", "dispatcher", "err", err)
		return out
	}
	for i := range exps {
		out[exps[i].AgentConfigID] = &exps[i]
	}
	return out
}

// resolveExperiment returns the effective config and arm for this task. The
// arm is decided on the task's first dispatch under the experiment and then
// read back from the stored assignment (sticky). It fails open to control.
func (d *Dispatcher) resolveExperiment(ctx context.Context, t gen.Task, cfg gen.AgentConfig, exp *gen.AgentExperiment) (gen.AgentConfig, experimentArm) {
	if exp == nil {
		return cfg, experimentArm{}
	}
	log := slog.With("component", "dispatcher", "task_id", t.ID, "experiment_id", exp.ID)
	arm, err := d.q.GetAgentExperimentAssignment(ctx, gen.GetAgentExperimentAssignmentParams{ExperimentID: exp.ID, TaskID: t.ID})
	if err != nil {
		cid := cfg.ID
		had, herr := d.q.HasAgentRunBeforeExperiment(ctx, gen.HasAgentRunBeforeExperimentParams{TaskID: t.ID, AgentConfigID: &cid, ID: exp.ID})
		if herr != nil {
			log.Warn("dispatcher: experiment pre-existing run check failed", "err", herr)
			return cfg, experimentArm{}
		}
		if had {
			return cfg, experimentArm{} // already in flight before the experiment: not enrolled
		}
		if ierr := d.q.InsertAgentExperimentAssignment(ctx, gen.InsertAgentExperimentAssignmentParams{
			ExperimentID: exp.ID, TaskID: t.ID, Arm: assignArm(exp.ID, t.ID, exp.SharePercent),
		}); ierr != nil {
			log.Warn("dispatcher: record experiment assignment", "err", ierr)
			return cfg, experimentArm{}
		}
		arm, err = d.q.GetAgentExperimentAssignment(ctx, gen.GetAgentExperimentAssignmentParams{ExperimentID: exp.ID, TaskID: t.ID})
		if err != nil {
			log.Warn("dispatcher: read experiment assignment", "err", err)
			return cfg, experimentArm{}
		}
	}
	if arm == ArmCandidate {
		var snap ConfigSnapshot
		if err := json.Unmarshal([]byte(exp.CandidateSnapshot), &snap); err != nil {
			log.Warn("dispatcher: corrupt experiment candidate snapshot, running control", "err", err)
			return cfg, experimentArm{ExperimentID: exp.ID, Arm: ArmControl}
		}
		return snap.ApplyTo(cfg), experimentArm{ExperimentID: exp.ID, Arm: ArmCandidate}
	}
	return cfg, experimentArm{ExperimentID: exp.ID, Arm: ArmControl}
}

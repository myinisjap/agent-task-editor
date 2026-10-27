package handlers

import (
	"testing"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

func TestComputeExperimentResults(t *testing.T) {
	cfg := "cfg"
	exp := "exp"
	arm := "candidate"
	t0 := time.Now()
	t1 := t0.Add(60 * time.Second)
	var tasks []gen.ListAgentExperimentTasksRow
	var runs []gen.ListAgentExperimentTaskRunsRow
	var hist []gen.ListAgentExperimentLabelHistoryRow
	// Candidate: 10 done tasks. t0 has two runs (not first pass) + rework +
	// human touch; t1..t9 are clean first passes costing 1.0 each.
	for i := 0; i < 10; i++ {
		id := string(rune('a' + i))
		tasks = append(tasks, gen.ListAgentExperimentTasksRow{TaskID: id, Arm: "candidate", IsTerminal: 1})
		n := 1
		if i == 0 {
			n = 2
		}
		for j := 0; j < n; j++ {
			st := "completed"
			if i == 0 && j == 0 {
				st = "failed"
			}
			runs = append(runs, gen.ListAgentExperimentTaskRunsRow{
				TaskID: id, AgentConfigID: &cfg, Status: st, CostUsd: 1,
				StartedAt: &t0, CompletedAt: &t1, ExperimentID: &exp, ExperimentArm: &arm,
			})
		}
		hist = append(hist, gen.ListAgentExperimentLabelHistoryRow{TaskID: id, ToLabel: "next", Trigger: "agent"})
	}
	hist = append(hist,
		gen.ListAgentExperimentLabelHistoryRow{TaskID: "a", ToLabel: "next", Trigger: "human"}, // rework + human
	)
	// Control: 9 done tasks (not enough), plus one not-done and one archived.
	for i := 0; i < 9; i++ {
		id := "c" + string(rune('a'+i))
		tasks = append(tasks, gen.ListAgentExperimentTasksRow{TaskID: id, Arm: "control", IsTerminal: 1})
	}
	tasks = append(tasks,
		gen.ListAgentExperimentTasksRow{TaskID: "open", Arm: "control"},
		gen.ListAgentExperimentTasksRow{TaskID: "arch", Arm: "control", Archived: 1, IsTerminal: 1},
	)

	res := computeExperimentResults(exp, cfg, tasks, runs, hist)
	c := res.Candidate
	if c.Tasks != 10 || c.TasksDone != 10 || c.NotEnoughData {
		t.Fatalf("candidate counts: %+v", c)
	}
	if c.FirstPassSuccessRatePercent != 90 || c.ReworkRatePercent != 10 || c.HumanTouchRatePercent != 10 {
		t.Fatalf("candidate rates: %+v", c)
	}
	if c.AvgCostToDoneUSD != 1.1 || c.AvgDurationSecs != 60 {
		t.Fatalf("candidate cost/duration: %+v", c)
	}
	k := res.Control
	if k.Tasks != 10 || k.TasksDone != 9 || !k.NotEnoughData || res.MinTasksPerArm != 10 {
		t.Fatalf("control: %+v", k)
	}
}

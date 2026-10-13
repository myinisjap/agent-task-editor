package storage

import "context"

// RecoverInterruptedRuns is the boot-time sweep for runs the previous process
// never finished (crash, SIGKILL after the drain window, power loss). Every
// run left running or pending is marked interrupted, not failed: the process
// dying is not the task's fault, so it must not count as a genuine failure in
// metrics or bump the consecutive-failure / transient-retry counters (this
// function deliberately touches no task counters). It then clears every
// task's active_agent_run_id, because nothing is genuinely active in a fresh
// process, so the interrupted tasks become dispatchable again on the next
// sweep. It returns the number of runs marked interrupted.
func RecoverInterruptedRuns(ctx context.Context, db *DB) (int64, error) {
	res, err := db.SQL().ExecContext(ctx,
		`UPDATE agent_runs
		 SET status='interrupted', completed_at=CURRENT_TIMESTAMP,
		     notes=COALESCE(notes, 'Run interrupted by a server restart; it will be re-dispatched.')
		 WHERE status IN ('running','pending')`)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if _, err := db.SQL().ExecContext(ctx,
		`UPDATE tasks SET active_agent_run_id = NULL WHERE active_agent_run_id IS NOT NULL`); err != nil {
		return n, err
	}
	return n, nil
}

package notify

// publisher is the single-method interface every event producer in this
// codebase depends on (workflow.Publisher, agent.Publisher, ghsync.Publisher,
// tasksource.Publisher, schedule.Publisher all have this exact method set).
type publisher interface {
	Publish(eventType string, payload map[string]any)
}

// MultiPublisher fans a single Publish call out to every publisher it wraps.
// It exists so the outbound webhook Notifier can be wired in as a second
// "subscriber" alongside the WS hub without the Hub itself needing a
// subscriber mechanism: main.go constructs one MultiPublisher wrapping
// [hub, notifier] (or just [hub] when notifications are disabled) and passes
// it everywhere a *ws.Hub used to go directly. Because every consumer takes
// its own named Publisher interface with an identical method set, a single
// concrete *MultiPublisher value satisfies all of them.
type MultiPublisher struct {
	targets []publisher
}

// FanOut creates a MultiPublisher that forwards every Publish call to each
// of targets in order. nil targets are skipped (so callers can pass a
// possibly-nil notifier without a conditional).
func FanOut(targets ...publisher) *MultiPublisher {
	mp := &MultiPublisher{}
	for _, t := range targets {
		if t == nil {
			continue
		}
		mp.targets = append(mp.targets, t)
	}
	return mp
}

// Publish forwards to every wrapped publisher. Each target is responsible
// for its own non-blocking behavior (both ws.Hub.Publish and
// Notifier.Publish already are), so this itself never blocks.
func (mp *MultiPublisher) Publish(eventType string, payload map[string]any) {
	for _, t := range mp.targets {
		t.Publish(eventType, payload)
	}
}

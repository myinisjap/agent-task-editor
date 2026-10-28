package agent

import (
	"sync/atomic"
	"time"
)

var lastLogSeq atomic.Int64

// NextLogSeq returns a process-wide strictly increasing sequence number for
// agent log entries. It is microsecond based so it stays roughly time-aligned
// across restarts and within JS's safe integer range.
func NextLogSeq() int64 {
	for {
		last := lastLogSeq.Load()
		n := time.Now().UnixMicro()
		if n <= last {
			n = last + 1
		}
		if lastLogSeq.CompareAndSwap(last, n) {
			return n
		}
	}
}

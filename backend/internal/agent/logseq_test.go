package agent

import (
	"sync"
	"testing"
)

func TestNextLogSeqUniqueIncreasing(t *testing.T) {
	prev := NextLogSeq()
	for i := 0; i < 1000; i++ {
		n := NextLogSeq()
		if n <= prev {
			t.Fatalf("not increasing: %d <= %d", n, prev)
		}
		prev = n
	}
	var mu sync.Mutex
	seen := map[int64]bool{}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				n := NextLogSeq()
				mu.Lock()
				if seen[n] {
					t.Errorf("duplicate %d", n)
				}
				seen[n] = true
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
}

package sweeper_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/job"
)

// locker grants the sweep lock when free is set and counts what Run did with it.
type locker struct {
	mu                 sync.Mutex
	free               bool
	tries, held, freed int
}

func (l *locker) TryLock(_ context.Context, key int64) (func(), bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.tries++
	if key != job.SweepLock || !l.free {
		return nil, false, nil
	}
	l.held++
	return func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.freed++
	}, true, nil
}

func (l *locker) counts() (tries, held, freed int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.tries, l.held, l.freed
}

// TestRunSkipsWithoutTheLock: a process that does not get the sweep lock does not sweep;
// one that gets it sweeps and releases it after each sweep.
func TestRunSkipsWithoutTheLock(t *testing.T) {
	for _, free := range []bool{false, true} {
		r := newRig(t)
		r.opts.Clock = func() time.Time { return t0.Add(48 * time.Hour) }
		r.cfg.Retention.SweepInterval = 5 * time.Millisecond
		l := &locker{free: free}
		r.opts.Lock = l
		expired := r.submit(t, "app", job.RetentionNone, t0)

		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			defer close(done)
			r.sweeper().Run(ctx)
		}()
		deadline := time.Now().Add(5 * time.Second)
		for {
			if tries, _, _ := l.counts(); tries >= 3 || time.Now().After(deadline) {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		cancel()
		<-done
		tries, held, freed := l.counts()
		if gone := !r.exists(t, expired.ID); gone != free {
			t.Errorf("lock free %t: expired job swept %t", free, gone)
		}
		if tries < 3 || (free && (held != tries || freed != held)) || (!free && held != 0) {
			t.Errorf("lock free %t: %d tries, %d held, %d released", free, tries, held, freed)
		}
	}
}

// TestSQLiteLockIsFree: on SQLite one process owns the database, so the lock is always
// granted, also twice.
func TestSQLiteLockIsFree(t *testing.T) {
	r := newRig(t)
	release1, ok1, err1 := r.store.TryLock(t.Context(), job.SweepLock)
	release2, ok2, err2 := r.store.TryLock(t.Context(), job.SweepLock)
	if !ok1 || !ok2 || err1 != nil || err2 != nil {
		t.Fatalf("TryLock = %t %v, %t %v; want granted", ok1, err1, ok2, err2)
	}
	release1()
	release2()
}

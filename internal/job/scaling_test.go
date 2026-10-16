package job

import (
	"errors"
	"testing"
	"time"
)

// TestUsageColumns: RecordAttempt and Finish store the token usage, which never
// decreases, and every read returns it.
func TestUsageColumns(t *testing.T) {
	forEachDriver(t, func(t *testing.T, s *sqlStore) {
		ctx := t.Context()
		seedApp(t, s, testApp("app"))
		j := createJob(t, s, newJob("app"))
		if _, err := s.LeaseNextQueued(ctx, "w", t0.Add(time.Second), t0.Add(time.Minute), nil); err != nil {
			t.Fatal(err)
		}
		if err := s.RecordAttempt(ctx, j.ID, "w", Progress{Attempts: 1, InputTokens: 1200, OutputTokens: 300}, t0.Add(2*time.Minute)); err != nil {
			t.Fatal(err)
		}
		got, err := s.GetJobAny(ctx, j.ID)
		if err != nil || got.InputTokens != 1200 || got.OutputTokens != 300 {
			t.Fatalf("after RecordAttempt: %d/%d, %v; want 1200/300", got.InputTokens, got.OutputTokens, err)
		}
		// A stale writer with lower counts does not lower them.
		if err := s.RecordAttempt(ctx, j.ID, "w", Progress{Attempts: 1, InputTokens: 10, OutputTokens: 1}, t0.Add(2*time.Minute)); err != nil {
			t.Fatal(err)
		}
		if err := s.Finish(ctx, j.ID, "w", Outcome{Status: StatusAccepted, Attempts: 2, InputTokens: 2500, OutputTokens: 650}, t0.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		list, err := s.ListJobs(ctx, JobFilter{ApplicationID: "app"}, 0)
		if err != nil || len(list) != 1 || list[0].InputTokens != 2500 || list[0].OutputTokens != 650 {
			t.Fatalf("after Finish: %+v, %v; want 2500/650", list, err)
		}
	})
}

// TestOldestQueuedAt: the created_at of the oldest queued job, zero when none is queued;
// running and terminal jobs do not count.
func TestOldestQueuedAt(t *testing.T) {
	forEachDriver(t, func(t *testing.T, s *sqlStore) {
		ctx := t.Context()
		if got, err := s.OldestQueuedAt(ctx); err != nil || !got.IsZero() {
			t.Fatalf("empty queue: %v, %v; want the zero time", got, err)
		}
		seedApp(t, s, testApp("app"))
		for i := range 3 {
			j := newJob("app")
			j.CreatedAt = t0.Add(time.Duration(i) * time.Minute)
			createJob(t, s, j)
		}
		if _, err := s.LeaseNextQueued(ctx, "w", t0.Add(time.Hour), t0.Add(2*time.Hour), nil); err != nil {
			t.Fatal(err) // the oldest is running now
		}
		if got, err := s.OldestQueuedAt(ctx); err != nil || !got.Equal(t0.Add(time.Minute)) {
			t.Errorf("OldestQueuedAt = %v, %v; want %v", got, err, t0.Add(time.Minute))
		}
	})
}

// TestIncrementRate: a fixed-window counter per application, kind and window, capped at
// the limit, and removed by DeleteRateBefore.
func TestIncrementRate(t *testing.T) {
	forEachDriver(t, func(t *testing.T, s *sqlStore) {
		ctx := t.Context()
		w := t0.Truncate(time.Minute)
		for i := 1; i <= 3; i++ {
			if n, err := s.IncrementRate(ctx, "app", RateRequests, w, 3); err != nil || n != i {
				t.Fatalf("request %d: %d, %v", i, n, err)
			}
		}
		if _, err := s.IncrementRate(ctx, "app", RateRequests, w, 3); !errors.Is(err, ErrRateLimited) {
			t.Fatalf("fourth request: %v, want ErrRateLimited", err)
		}
		for _, other := range []struct {
			app, kind string
			start     time.Time
		}{{"app", RateSubmits, w}, {"other", RateRequests, w}, {"app", RateRequests, w.Add(time.Minute)}} {
			if n, err := s.IncrementRate(ctx, other.app, other.kind, other.start, 3); err != nil || n != 1 {
				t.Errorf("%+v: %d, %v; want a counter of its own", other, n, err)
			}
		}
		if _, err := s.IncrementRate(ctx, "app", RateRequests, w, 0); !errors.Is(err, ErrRateLimited) {
			t.Errorf("limit 0: %v, want ErrRateLimited", err)
		}
		if n, err := s.DeleteRateBefore(ctx, w.Add(time.Minute)); err != nil || n != 3 {
			t.Errorf("DeleteRateBefore = %d, %v; want the 3 rows of the first window", n, err)
		}
	})
}

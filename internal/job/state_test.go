package job

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"testing"
	"time"
)

func TestStatusTerminal(t *testing.T) {
	for s, want := range map[Status]bool{
		StatusQueued: false, StatusRunning: false, StatusAccepted: true, StatusRejected: true, StatusFailed: true,
	} {
		if s.Terminal() != want {
			t.Errorf("%s.Terminal() = %v", s, !want)
		}
	}
}

func TestHappyPath(t *testing.T) {
	forEachDriver(t, func(t *testing.T, s *sqlStore) {
		ctx := t.Context()
		seedApp(t, s, testApp("app"))
		j := createJob(t, s, newJob("app"))

		leased, err := s.LeaseNextQueued(ctx, "w1", t0.Add(time.Second), t0.Add(time.Minute), nil)
		if err != nil {
			t.Fatal(err)
		}
		if leased.ID != j.ID || leased.Status != StatusRunning || leased.WorkerID != "w1" ||
			!leased.StartedAt.Equal(t0.Add(time.Second)) || !leased.LeaseUntil.Equal(t0.Add(time.Minute)) {
			t.Errorf("leased = %+v", leased)
		}
		if _, err := s.LeaseNextQueued(ctx, "w2", t0.Add(2*time.Second), t0.Add(time.Minute), nil); !errors.Is(err, ErrNotFound) {
			t.Errorf("second lease: %v, want ErrNotFound", err)
		}

		p := Progress{Attempts: 1, Result: json.RawMessage(`{"a":1}`), Feedback: json.RawMessage(`[{"pointer":"/a","message":"m","source":"judge"}]`)}
		if err := s.RecordAttempt(ctx, j.ID, "w1", p, t0.Add(2*time.Minute)); err != nil {
			t.Fatal(err)
		}
		if err := s.RecordAttempt(ctx, j.ID, "w2", p, t0.Add(2*time.Minute)); !errors.Is(err, ErrConflict) {
			t.Errorf("RecordAttempt by another worker: %v, want ErrConflict", err)
		}
		if err := s.RenewLease(ctx, j.ID, "w1", t0.Add(3*time.Minute)); err != nil {
			t.Fatal(err)
		}
		got, _ := s.GetJobAny(ctx, j.ID)
		if got.Attempts != 1 || string(got.Result) != `{"a":1}` || !got.LeaseUntil.Equal(t0.Add(3*time.Minute)) {
			t.Errorf("after attempt = %+v", got)
		}

		confidence := 0.75
		o := Outcome{Status: StatusAccepted, Attempts: 2, Result: json.RawMessage(`{"a":2}`), Feedback: json.RawMessage(`[]`), JudgeConfidence: &confidence}
		if err := s.Finish(ctx, j.ID, "w1", o, t0.Add(10*time.Second)); err != nil {
			t.Fatal(err)
		}
		got, _ = s.GetJobAny(ctx, j.ID)
		if got.Status != StatusAccepted || got.Attempts != 2 || string(got.Result) != `{"a":2}` || string(got.Feedback) != `[]` ||
			got.Error != nil || got.JudgeConfidence == nil || *got.JudgeConfidence != 0.75 ||
			!got.FinishedAt.Equal(t0.Add(10*time.Second)) || !got.LeaseUntil.IsZero() {
			t.Errorf("finished = %+v", got)
		}
		if err := s.RenewLease(ctx, j.ID, "w1", t0.Add(time.Hour)); !errors.Is(err, ErrConflict) {
			t.Errorf("RenewLease after finish: %v, want ErrConflict", err)
		}
	})
}

func TestTerminalImmutable(t *testing.T) {
	forEachDriver(t, func(t *testing.T, s *sqlStore) {
		ctx := t.Context()
		seedApp(t, s, testApp("app"))
		j := createJob(t, s, newJob("app"))
		failed := Outcome{Status: StatusFailed, Attempts: 0, Error: json.RawMessage(`{"code":"timeout","message":"m"}`), Feedback: json.RawMessage(`[]`)}

		if err := s.Finish(ctx, j.ID, "", failed, t0); !errors.Is(err, ErrConflict) {
			t.Errorf("Finish of a queued job: %v, want ErrConflict", err)
		}
		finishJob(t, s, j.ID, StatusRejected)
		for _, o := range []Outcome{failed, {Status: StatusAccepted, Attempts: 5}} {
			if err := s.Finish(ctx, j.ID, "", o, t0.Add(time.Hour)); !errors.Is(err, ErrConflict) {
				t.Errorf("Finish(%s) of a terminal job: %v, want ErrConflict", o.Status, err)
			}
		}
		if err := s.RecordAttempt(ctx, j.ID, "finisher", Progress{Attempts: 9}, t0.Add(time.Hour)); !errors.Is(err, ErrConflict) {
			t.Errorf("RecordAttempt on a terminal job: %v, want ErrConflict", err)
		}
		if _, err := s.LeaseNextQueued(ctx, "w", t0.Add(time.Hour), t0.Add(2*time.Hour), nil); !errors.Is(err, ErrNotFound) {
			t.Errorf("terminal job leased: %v", err)
		}
		if err := s.Finish(ctx, j.ID, "", Outcome{Status: StatusRunning}, t0); err == nil {
			t.Error("Finish accepted a non-terminal status")
		}
		if err := s.Finish(ctx, "j_missing", "", failed, t0); !errors.Is(err, ErrNotFound) {
			t.Errorf("Finish of a missing job: %v, want ErrNotFound", err)
		}
		got, _ := s.GetJobAny(ctx, j.ID)
		if got.Status != StatusRejected || got.Attempts != 1 {
			t.Errorf("terminal row changed: %+v", got)
		}
	})
}

// TestReleaseAfterCrash: a worker records one attempt, then disappears. Once its lease
// passed, another worker leases the job again with the persisted attempts and candidate.
func TestReleaseAfterCrash(t *testing.T) {
	forEachDriver(t, func(t *testing.T, s *sqlStore) {
		ctx := t.Context()
		seedApp(t, s, testApp("app"))
		j := createJob(t, s, newJob("app"))
		if _, err := s.LeaseNextQueued(ctx, "crashed", t0, t0.Add(time.Minute), nil); err != nil {
			t.Fatal(err)
		}
		p := Progress{Attempts: 2, Result: json.RawMessage(`{"a":1}`), Feedback: json.RawMessage(`[{"pointer":"","message":"m","source":"schema"}]`)}
		if err := s.RecordAttempt(ctx, j.ID, "crashed", p, t0.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}

		// Lease still live: nothing to take, and the live lease counts against the ceiling.
		if _, err := s.LeaseNextQueued(ctx, "next", t0.Add(30*time.Second), t0.Add(2*time.Minute), nil); !errors.Is(err, ErrNotFound) {
			t.Fatalf("live lease taken: %v", err)
		}
		leased, err := s.LeaseNextQueued(ctx, "next", t0.Add(61*time.Second), t0.Add(2*time.Minute), nil)
		if err != nil {
			t.Fatal(err)
		}
		if leased.ID != j.ID || leased.WorkerID != "next" || leased.Attempts != 2 || string(leased.Result) != `{"a":1}` ||
			string(leased.Feedback) != string(p.Feedback) || !leased.StartedAt.Equal(t0) {
			t.Errorf("re-leased = %+v", leased)
		}
		// The crashed worker lost the job.
		if err := s.RecordAttempt(ctx, j.ID, "crashed", Progress{Attempts: 3}, t0.Add(3*time.Minute)); !errors.Is(err, ErrConflict) {
			t.Errorf("stale worker recorded: %v", err)
		}
		if err := s.Finish(ctx, j.ID, "crashed", Outcome{Status: StatusAccepted, Attempts: 3}, t0.Add(time.Minute)); !errors.Is(err, ErrConflict) {
			t.Errorf("stale worker finished: %v", err)
		}
		// Attempts never decrease.
		if err := s.RecordAttempt(ctx, j.ID, "next", Progress{Attempts: 1}, t0.Add(3*time.Minute)); !errors.Is(err, ErrConflict) {
			t.Errorf("attempts decreased: %v", err)
		}
		if err := s.RecordAttempt(ctx, j.ID, "next", Progress{Attempts: 3, Result: p.Result}, t0.Add(3*time.Minute)); err != nil {
			t.Fatal(err)
		}
		if err := s.Finish(ctx, j.ID, "next", Outcome{Status: StatusRejected, Attempts: 1}, t0.Add(2*time.Minute)); err != nil {
			t.Fatal(err)
		}
		if got, _ := s.GetJobAny(ctx, j.ID); got.Attempts != 3 {
			t.Errorf("Finish lowered attempts to %d", got.Attempts)
		}
	})
}

// TestStateMachineProperty runs random sequences of store operations over a few jobs
// and checks after each step that no terminal status ever changed, attempts never
// decreased, and finished_at is set exactly on terminal rows.
func TestStateMachineProperty(t *testing.T) {
	const seeds, steps, jobs = 20, 60, 3
	workers := []string{"w1", "w2", ""}
	statuses := []Status{StatusAccepted, StatusRejected, StatusFailed, StatusRunning, StatusQueued}
	for _, d := range testDrivers {
		t.Run(d.name, func(t *testing.T) {
			if d.name == "postgres" {
				postgresDSN(t) // skips without a database rather than failing the vacuity check
			}
			testStateMachineProperty(t, d.open, seeds, steps, jobs, workers, statuses)
		})
	}
}

func testStateMachineProperty(t *testing.T, open func(*testing.T) *sqlStore, seeds, steps, jobs int, workers []string, statuses []Status) {
	terminal := 0 // rows that reached a terminal status, so the property is not vacuous
	for seed := range uint64(seeds) {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			r := rand.New(rand.NewPCG(seed, 2026))
			s := open(t)
			ctx := t.Context()
			seedApp(t, s, testApp("app"))
			var ids []string
			for range jobs {
				ids = append(ids, createJob(t, s, newJob("app")).ID)
			}
			seen := map[string]Job{}
			now := t0
			for step := range steps {
				now = now.Add(time.Duration(r.IntN(90)) * time.Second)
				id := ids[r.IntN(len(ids))]
				worker := workers[r.IntN(len(workers))]
				var op string
				var err error
				switch r.IntN(6) {
				case 0, 1:
					op = "lease"
					_, err = s.LeaseNextQueued(ctx, worker, now, now.Add(time.Duration(1+r.IntN(120))*time.Second),
						map[string]int{"app": r.IntN(3)})
				case 2:
					op = "record"
					err = s.RecordAttempt(ctx, id, worker, Progress{Attempts: r.IntN(5)}, now.Add(time.Minute))
				case 3:
					op = "renew"
					err = s.RenewLease(ctx, id, worker, now.Add(time.Minute))
				case 4:
					op = "finish"
					err = s.Finish(ctx, id, worker, Outcome{Status: statuses[r.IntN(len(statuses))], Attempts: r.IntN(5)}, now)
				case 5:
					op = "delete-content"
					err = s.MarkContentDeleted(ctx, id, Content(1+r.IntN(2)), now)
				}
				if err != nil && !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrConflict) && !errors.Is(err, errNotTerminal) {
					t.Fatalf("step %d %s: unexpected error %v", step, op, err)
				}
				for _, id := range ids {
					cur, err := s.GetJobAny(ctx, id)
					if err != nil {
						t.Fatal(err)
					}
					prev, ok := seen[id]
					if ok && prev.Status.Terminal() && cur.Status != prev.Status {
						t.Fatalf("step %d %s: terminal %s changed to %s", step, op, prev.Status, cur.Status)
					}
					if ok && cur.Attempts < prev.Attempts {
						t.Fatalf("step %d %s: attempts %d decreased to %d", step, op, prev.Attempts, cur.Attempts)
					}
					if cur.Status.Terminal() == cur.FinishedAt.IsZero() {
						t.Fatalf("step %d %s: status %s with finished_at %v", step, op, cur.Status, cur.FinishedAt)
					}
					seen[id] = cur
				}
			}
			for _, j := range seen {
				if j.Status.Terminal() {
					terminal++
				}
			}
		})
	}
	if terminal == 0 {
		t.Error("no random sequence reached a terminal status")
	}
}

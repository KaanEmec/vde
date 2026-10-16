package worker_test

import (
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/job"
	"github.com/kaanemec/vde/internal/worker"
)

func closed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func TestNotifierWakesEverySubscriberOfTheJob(t *testing.T) {
	t.Parallel()
	n := worker.NewNotifier()
	a, cancelA := n.Subscribe("j_1")
	defer cancelA()
	b, cancelB := n.Subscribe("j_1")
	other, cancelOther := n.Subscribe("j_2")
	defer cancelOther()
	cancelB() // a dropped subscription is not closed
	n.Notify("j_1")
	if !closed(a) || closed(b) || closed(other) {
		t.Errorf("closed: a %v, b %v, other %v; want a only", closed(a), closed(b), closed(other))
	}
	n.Notify("j_1") // nothing subscribed any more: no double close
	n.Notify("j_unknown")
}

// The pool calls OnFinish after the terminal write, with the terminal status and
// finished_at, which is what the Notifier and the webhooks are driven by.
func TestOnFinishRunsAfterTheTerminalWrite(t *testing.T) {
	ext, judge := accepting()
	n := worker.NewNotifier()
	got := make(chan job.Job, 1)
	var r *rig
	r = newRig(t, ext, judge, worker.Options{OnFinish: func(j job.Job) {
		if row, err := r.store.GetJobAny(t.Context(), j.ID); err != nil || !row.Status.Terminal() {
			t.Errorf("row at OnFinish = %+v, %v; want terminal", row.Status, err)
		}
		n.Notify(j.ID)
		got <- j
	}})
	j := r.submit(t, "app", nil)
	done, cancel := n.Subscribe(j.ID)
	defer cancel()
	r.start(t)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("not notified")
	}
	f := <-got
	if f.Status != job.StatusAccepted || f.FinishedAt.IsZero() || f.ID != j.ID {
		t.Errorf("OnFinish job = %+v", f)
	}
}

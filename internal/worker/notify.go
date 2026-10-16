package worker

import "sync"

// Notifier tells in-process waiters that a job reached its terminal state: POST
// /v1/extract subscribes to the job it created, and the pool's OnFinish hook calls
// Notify after the terminal write. It is only a shortcut: a waiter must still read the
// store, which is also how it learns of a job another replica finished.
type Notifier struct {
	mu   sync.Mutex
	subs map[string][]chan struct{}
}

// NewNotifier returns an empty notifier.
func NewNotifier() *Notifier { return &Notifier{subs: map[string][]chan struct{}{}} }

// Subscribe returns a channel closed when Notify(jobID) is called, and a cancel
// function that drops the subscription (call it when the wait ends without one).
func (n *Notifier) Subscribe(jobID string) (<-chan struct{}, func()) {
	ch := make(chan struct{})
	n.mu.Lock()
	n.subs[jobID] = append(n.subs[jobID], ch)
	n.mu.Unlock()
	return ch, func() {
		n.mu.Lock()
		defer n.mu.Unlock()
		subs := n.subs[jobID]
		for i, c := range subs {
			if c == ch {
				subs = append(subs[:i], subs[i+1:]...)
				break
			}
		}
		if len(subs) == 0 {
			delete(n.subs, jobID)
		} else {
			n.subs[jobID] = subs
		}
	}
}

// Notify closes every channel subscribed to jobID and forgets them. It never blocks.
func (n *Notifier) Notify(jobID string) {
	n.mu.Lock()
	subs := n.subs[jobID]
	delete(n.subs, jobID)
	n.mu.Unlock()
	for _, ch := range subs {
		close(ch)
	}
}

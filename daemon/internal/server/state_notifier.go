package server

import "sync"

type stateNotifier struct {
	mu     sync.Mutex
	nextID uint64
	subs   map[uint64]chan struct{}
}

func newStateNotifier() *stateNotifier {
	return &stateNotifier{subs: make(map[uint64]chan struct{})}
}

func (n *stateNotifier) Subscribe() (uint64, <-chan struct{}) {
	if n == nil {
		return 0, nil
	}

	n.mu.Lock()
	defer n.mu.Unlock()

	n.nextID++
	id := n.nextID
	ch := make(chan struct{}, 1)
	n.subs[id] = ch
	return id, ch
}

func (n *stateNotifier) Unsubscribe(id uint64) {
	if n == nil || id == 0 {
		return
	}

	n.mu.Lock()
	ch, ok := n.subs[id]
	if ok {
		delete(n.subs, id)
	}
	n.mu.Unlock()

	if ok {
		close(ch)
	}
}

func (n *stateNotifier) Notify() {
	if n == nil {
		return
	}

	n.mu.Lock()
	defer n.mu.Unlock()
	for _, ch := range n.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

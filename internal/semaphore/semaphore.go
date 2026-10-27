package semaphore

type Semaphore struct {
	ch chan struct{}
}

func New(n int) *Semaphore {
	return &Semaphore{ch: make(chan struct{}, n)}
}

// Acquire takes a slot, waiting for one if necessary, and reports whether it
// got it. It returns false once done is closed — including when a slot happens
// to be free at the same moment, so a cancelled caller never starts new work.
func (s *Semaphore) Acquire(done <-chan struct{}) bool {
	if isDone(done) {
		return false
	}
	select {
	case s.ch <- struct{}{}:
		// A select picks at random when a slot frees up as done closes, so
		// check again rather than let a cancelled caller through.
		if isDone(done) {
			s.Release()
			return false
		}
		return true
	case <-done:
		return false
	}
}

func isDone(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
}

func (s *Semaphore) Release() {
	<-s.ch
}

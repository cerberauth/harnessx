package semaphore

type Semaphore struct {
	ch chan struct{}
}

func New(n int) *Semaphore {
	return &Semaphore{ch: make(chan struct{}, n)}
}

func (s *Semaphore) Acquire(done <-chan struct{}) bool {
	select {
	case s.ch <- struct{}{}:
		return true
	case <-done:
		return false
	}
}

func (s *Semaphore) Release() {
	<-s.ch
}

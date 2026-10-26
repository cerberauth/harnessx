package semaphore

import "testing"

func TestAcquire_ReturnsFalseOnceDoneEvenIfASlotIsFree(t *testing.T) {
	s := New(1)
	done := make(chan struct{})
	close(done)

	for range 100 {
		if s.Acquire(done) {
			t.Fatal("Acquire succeeded with done closed and a free slot")
		}
	}
}

func TestAcquire_TakesAndReleasesSlots(t *testing.T) {
	s := New(1)
	if !s.Acquire(nil) {
		t.Fatal("first Acquire failed")
	}
	done := make(chan struct{})
	close(done)
	if s.Acquire(done) {
		t.Error("second Acquire succeeded with the only slot taken and done closed")
	}
	s.Release()
	if !s.Acquire(nil) {
		t.Error("Acquire failed after Release")
	}
}

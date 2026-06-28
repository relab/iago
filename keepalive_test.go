package iago

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// fakePinger records SendRequest calls and returns a configurable error, so the
// keepalive loop can be exercised without a live SSH connection.
type fakePinger struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (f *fakePinger) SendRequest(name string, wantReply bool, payload []byte) (bool, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return false, nil, f.err
}

func (f *fakePinger) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestKeepAliveOption(t *testing.T) {
	if got := applyGroupOptions(KeepAlive(30 * time.Second)).keepAliveInterval; got != 30*time.Second {
		t.Errorf("keepAliveInterval = %v, want 30s", got)
	}
	if got := applyGroupOptions().keepAliveInterval; got != 0 {
		t.Errorf("default keepAliveInterval = %v, want 0", got)
	}
}

// TestKeepAliveLoopOnDead verifies that a failing ping invokes onDead once and
// stops the loop, so a dead connection is torn down instead of hanging.
func TestKeepAliveLoopOnDead(t *testing.T) {
	p := &fakePinger{err: errors.New("connection dead")}
	tick := make(chan time.Time)
	done := make(chan struct{})
	dead := make(chan struct{}, 1)
	finished := make(chan struct{})
	go func() {
		keepAliveLoop(p, tick, func() { dead <- struct{}{} }, done)
		close(finished)
	}()

	tick <- time.Now()
	select {
	case <-dead:
	case <-time.After(time.Second):
		t.Fatal("onDead not called after ping failure")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("loop did not return after ping failure")
	}
}

// TestKeepAliveLoopStopsOnDone verifies that closing done stops the loop without
// reporting the connection dead.
func TestKeepAliveLoopStopsOnDone(t *testing.T) {
	p := &fakePinger{}
	tick := make(chan time.Time)
	done := make(chan struct{})
	var deadCalled bool
	finished := make(chan struct{})
	go func() {
		keepAliveLoop(p, tick, func() { deadCalled = true }, done)
		close(finished)
	}()

	close(done)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("loop did not return after done closed")
	}
	if deadCalled {
		t.Error("onDead should not be called on a clean stop")
	}
}

// TestKeepAliveLoopPingsUntilStopped verifies that successful pings keep the loop
// running across multiple ticks until done is closed.
func TestKeepAliveLoopPingsUntilStopped(t *testing.T) {
	p := &fakePinger{}
	tick := make(chan time.Time)
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		keepAliveLoop(p, tick, nil, done)
		close(finished)
	}()

	// Each send returns only after the loop receives the previous tick, so two
	// successful sends prove the loop kept running rather than exiting early.
	tick <- time.Now()
	tick <- time.Now()
	close(done)
	<-finished

	if got := p.callCount(); got < 2 {
		t.Errorf("ping calls = %d, want >= 2", got)
	}
}

// TestStartKeepAliveStopIsIdempotent verifies the stop function can be called
// repeatedly (Close may run after the loop already exited) without panicking.
func TestStartKeepAliveStopIsIdempotent(t *testing.T) {
	stop := startKeepAlive(&fakePinger{}, time.Hour, nil)
	stop()
	stop()
}

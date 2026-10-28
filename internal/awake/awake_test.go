package awake

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/filememo"
)

func TestWant(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		st   State
		want bool
	}{
		{State{}, false},
		{State{Busy: true}, true},
		{State{Last: now.Add(-time.Minute)}, true},
		{State{Last: now.Add(-Grace - time.Second)}, false},
	} {
		if got := want(c.st, now); got != c.want {
			t.Errorf("want(%+v) = %v, want %v", c.st, got, c.want)
		}
	}
}

// TestKeep holds sleep off while the agents work and the setting is on, once,
// and lets go when they are idle, when it is turned off and when Keep ends.
func TestKeep(t *testing.T) {
	oldHold, oldTick := hold, tick
	defer func() { hold, tick = oldHold, oldTick }()
	tick = time.Millisecond
	var held, takes atomic.Int32
	hold = func(bool) (func(), error) {
		held.Add(1)
		takes.Add(1)
		return func() { held.Add(-1) }, nil
	}
	var mu sync.Mutex
	on, st := true, State{}
	// the setting is written as settings.Save writes it (filememo.Forget)
	set := func(o bool, s State) { mu.Lock(); on, st = o, s; mu.Unlock(); filememo.Forget() }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		Keep(ctx, func() Level {
			mu.Lock()
			defer mu.Unlock()
			if on {
				return System
			}
			return Off
		},
			func() State { mu.Lock(); defer mu.Unlock(); return st })
		close(done)
	}()
	wait := func(n int32, what string) {
		t.Helper()
		for end := time.Now().Add(2 * time.Second); held.Load() != n; {
			if time.Now().After(end) {
				t.Fatalf("%s: held %d, want %d", what, held.Load(), n)
			}
			time.Sleep(time.Millisecond)
		}
	}
	wait(0, "idle")
	set(true, State{Busy: true})
	wait(1, "busy")
	time.Sleep(20 * time.Millisecond)
	if takes.Load() != 1 {
		t.Fatalf("taken %d times while busy, want once", takes.Load())
	}
	set(true, State{Last: time.Now()})
	time.Sleep(20 * time.Millisecond)
	wait(1, "within grace")
	set(true, State{Last: time.Now().Add(-Grace - time.Minute)})
	wait(0, "past grace")
	set(false, State{Busy: true})
	time.Sleep(20 * time.Millisecond)
	wait(0, "off")
	set(true, State{Busy: true})
	wait(1, "on again")
	cancel()
	<-done
	wait(0, "ended")
}

// TestKeepDisplay holds the display on only at Display, and takes the hold
// anew when the level changes while the agents work (#975).
func TestKeepDisplay(t *testing.T) {
	oldHold, oldTick := hold, tick
	defer func() { hold, tick = oldHold, oldTick }()
	tick = time.Millisecond
	var mu sync.Mutex
	var holds []bool // display of each hold taken
	live := 0
	hold = func(display bool) (func(), error) {
		mu.Lock()
		defer mu.Unlock()
		holds = append(holds, display)
		live++
		return func() { mu.Lock(); live--; mu.Unlock() }, nil
	}
	level := System
	set := func(l Level) { mu.Lock(); level = l; mu.Unlock(); filememo.Forget() }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		Keep(ctx, func() Level { mu.Lock(); defer mu.Unlock(); return level },
			func() State { return State{Busy: true} })
		close(done)
	}()
	waitFor := func(what string, ok func() bool) {
		t.Helper()
		for end := time.Now().Add(2 * time.Second); ; {
			mu.Lock()
			good := ok()
			mu.Unlock()
			if good {
				return
			}
			if time.Now().After(end) {
				mu.Lock()
				defer mu.Unlock()
				t.Fatalf("%s: holds %v, live %d", what, holds, live)
			}
			time.Sleep(time.Millisecond)
		}
	}
	waitFor("system", func() bool { return len(holds) == 1 && !holds[0] && live == 1 })
	set(Display)
	waitFor("display", func() bool { return len(holds) == 2 && holds[1] && live == 1 })
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	if len(holds) != 2 {
		t.Errorf("taken again at the same level: %v", holds)
	}
	mu.Unlock()
	set(System)
	waitFor("system again", func() bool { return len(holds) == 3 && !holds[2] && live == 1 })
	set(Off)
	waitFor("off", func() bool { return live == 0 })
	cancel()
	<-done
}

// keepBeat is one timer Keep requested. The test chooses when it fires,
// so a shared clock boundary cannot be mistaken for an extra poll.
type keepBeat struct {
	every time.Duration
	fire  chan time.Time
}

func keepBeats(t *testing.T, ctx context.Context) func() keepBeat {
	t.Helper()
	waits := make(chan keepBeat, 1)
	old := nextBeat
	nextBeat = func(every time.Duration) <-chan time.Time {
		b := keepBeat{every: every, fire: make(chan time.Time, 1)}
		select {
		case waits <- b:
		case <-ctx.Done():
		}
		return b.fire
	}
	t.Cleanup(func() { nextBeat = old })
	return func() keepBeat {
		t.Helper()
		select {
		case b := <-waits:
			return b
		case <-time.After(2 * time.Second):
			t.Fatal("Keep did not reach its next wait")
			return keepBeat{}
		}
	}
}

// Turned off, Keep waits for writes and the slower external-change beat,
// not its active tick. A clock boundary legitimately reads the setting
// again even if it falls immediately after the first read.
func TestKeepOffWaitsForTheSetting(t *testing.T) {
	oldHold, oldTick := hold, tick
	t.Cleanup(func() { hold, tick = oldHold, oldTick })
	tick = time.Millisecond
	var held atomic.Int32
	hold = func(bool) (func(), error) { held.Add(1); return func() { held.Add(-1) }, nil }
	var looks atomic.Int32
	var on atomic.Bool
	ctx, cancel := context.WithCancel(context.Background())
	next := keepBeats(t, ctx)
	done := make(chan struct{})
	go func() {
		Keep(ctx, func() Level {
			looks.Add(1)
			if on.Load() {
				return System
			}
			return Off
		}, func() State { return State{Busy: true} })
		close(done)
	}()
	t.Cleanup(func() { cancel(); <-done })
	first := next()
	if first.every != filememo.OthersEvery() || looks.Load() != 1 || held.Load() != 0 {
		t.Fatalf("off: interval %s, looks %d, held %d", first.every, looks.Load(), held.Load())
	}
	// Cross the next shared clock boundary without writing anything.
	first.fire <- time.Now()
	second := next()
	if second.every != filememo.OthersEvery() || looks.Load() != 2 || held.Load() != 0 {
		t.Fatalf("external-change beat: interval %s, looks %d, held %d", second.every, looks.Load(), held.Load())
	}
	on.Store(true)
	filememo.Forget()
	// No timer fires here: the write alone must wake the loop.
	third := next()
	if third.every != tick || looks.Load() != 3 || held.Load() != 1 {
		t.Fatalf("setting enabled: interval %s, looks %d, held %d", third.every, looks.Load(), held.Load())
	}
}

// A write during the setting read closes the channel captured before it.
// Keep must read again without a timer wakeup.
func TestKeepOffSeesAWriteDuringTheLook(t *testing.T) {
	oldHold := hold
	t.Cleanup(func() { hold = oldHold })
	var held atomic.Int32
	hold = func(bool) (func(), error) { held.Add(1); return func() { held.Add(-1) }, nil }
	var looks atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	next := keepBeats(t, ctx)
	done := make(chan struct{})
	go func() {
		Keep(ctx, func() Level {
			if looks.Add(1) > 1 {
				return System
			}
			filememo.Forget()
			return Off
		}, func() State { return State{Busy: true} })
		close(done)
	}()
	t.Cleanup(func() { cancel(); <-done })
	first := next()
	if first.every != filememo.OthersEvery() {
		t.Fatalf("off interval %s, want %s", first.every, filememo.OthersEvery())
	}
	// Deliberately leave first.fire pending: the write must suffice.
	second := next()
	if second.every != tick || looks.Load() != 2 || held.Load() != 1 {
		t.Fatalf("write during read: interval %s, looks %d, held %d", second.every, looks.Load(), held.Load())
	}
}

package telegrambot

import (
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Unix(1_700_000_000, 0)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

type fakeTelegramTimerFactory struct {
	mu     sync.Mutex
	timers []*fakeTelegramTimer
}

type fakeTelegramTimer struct {
	duration time.Duration
	ch       chan time.Time
}

func (f *fakeTelegramTimerFactory) NewTimer(d time.Duration) telegramTimer {
	timer := &fakeTelegramTimer{
		duration: d,
		ch:       make(chan time.Time, 1),
	}
	f.mu.Lock()
	f.timers = append(f.timers, timer)
	f.mu.Unlock()
	return timer
}

func (f *fakeTelegramTimerFactory) WaitTimer(t *testing.T, index int) *fakeTelegramTimer {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		f.mu.Lock()
		if len(f.timers) > index {
			timer := f.timers[index]
			f.mu.Unlock()
			return timer
		}
		f.mu.Unlock()
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for fake timer %d", index)
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func (t *fakeTelegramTimer) C() <-chan time.Time {
	return t.ch
}

func (t *fakeTelegramTimer) Stop() bool {
	return true
}

func (t *fakeTelegramTimer) Fire(at time.Time) {
	t.ch <- at
}

type fakeTelegramTickerFactory struct {
	mu      sync.Mutex
	tickers []*fakeTelegramTicker
}

type fakeTelegramTicker struct {
	duration time.Duration
	ch       chan time.Time
}

func (f *fakeTelegramTickerFactory) NewTicker(d time.Duration) telegramTicker {
	ticker := &fakeTelegramTicker{
		duration: d,
		ch:       make(chan time.Time, 8),
	}
	f.mu.Lock()
	f.tickers = append(f.tickers, ticker)
	f.mu.Unlock()
	return ticker
}

func (f *fakeTelegramTickerFactory) WaitTicker(t *testing.T, index int) *fakeTelegramTicker {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		f.mu.Lock()
		if len(f.tickers) > index {
			ticker := f.tickers[index]
			f.mu.Unlock()
			return ticker
		}
		f.mu.Unlock()
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for fake ticker %d", index)
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func (t *fakeTelegramTicker) C() <-chan time.Time {
	return t.ch
}

func (t *fakeTelegramTicker) Stop() {}

func (t *fakeTelegramTicker) Tick(at time.Time) {
	t.ch <- at
}

func waitForTestCondition(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		if ok() {
			return
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for condition")
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

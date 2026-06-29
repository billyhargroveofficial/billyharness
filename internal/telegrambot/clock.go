package telegrambot

import "time"

type telegramTimer interface {
	C() <-chan time.Time
	Stop() bool
}

type telegramTicker interface {
	C() <-chan time.Time
	Stop()
}

type realTelegramTimer struct {
	timer *time.Timer
}

func (t realTelegramTimer) C() <-chan time.Time {
	return t.timer.C
}

func (t realTelegramTimer) Stop() bool {
	return t.timer.Stop()
}

type realTelegramTicker struct {
	ticker *time.Ticker
}

func (t realTelegramTicker) C() <-chan time.Time {
	return t.ticker.C
}

func (t realTelegramTicker) Stop() {
	t.ticker.Stop()
}

var telegramNow = time.Now

var newTelegramTimer = func(d time.Duration) telegramTimer {
	return realTelegramTimer{timer: time.NewTimer(d)}
}

var newTelegramTicker = func(d time.Duration) telegramTicker {
	return realTelegramTicker{ticker: time.NewTicker(d)}
}

package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync/atomic"
	"time"
)

var fallbackJobIDSequence atomic.Uint64

func newClientJobID() string {
	var random [16]byte
	if _, err := rand.Read(random[:]); err == nil {
		return "j-" + hex.EncodeToString(random[:])
	}
	return fmt.Sprintf("j-%x-%x", time.Now().UnixNano(), fallbackJobIDSequence.Add(1))
}

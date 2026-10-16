package id

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"sync/atomic"
	"time"
)

var counter atomic.Uint64

func fallbackHex(n int) string {
	c := counter.Add(1)
	seed := fmt.Sprintf("%016x%016x%08x", uint64(time.Now().UnixNano()), c, uint32(os.Getpid()))
	for len(seed) < n {
		seed += seed
	}
	if n <= 0 {
		return seed
	}
	return seed[:n]
}

func New() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fallbackHex(32)
	}
	return hex.EncodeToString(b[:])
}

func UUIDv4() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		raw, _ := hex.DecodeString(fallbackHex(32))
		copy(b[:], raw)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

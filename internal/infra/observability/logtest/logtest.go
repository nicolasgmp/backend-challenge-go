package logtest

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

type Buffer struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (b *Buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.Write(p)
}

func (b *Buffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.String()
}

func Leaked(logs string, forbidden ...string) []string {
	var found []string
	for _, value := range forbidden {
		if value != "" && strings.Contains(logs, value) {
			found = append(found, value)
		}
	}
	return found
}

func AssertNoLeak(tb testing.TB, logs string, forbidden ...string) {
	tb.Helper()

	if found := Leaked(logs, forbidden...); len(found) > 0 {
		tb.Errorf("the logs contain %d forbidden value(s): %q", len(found), found)
	}
}

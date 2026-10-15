//go:build !ort

package embed

import "testing"

func TestNewLocalUnavailableWithoutORT(t *testing.T) {
	if _, err := NewLocal(LocalConfig{Dir: "./models", Model: "x"}); err == nil {
		t.Fatal("NewLocal must fail in the default build")
	}
}

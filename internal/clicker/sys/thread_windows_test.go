//go:build windows

package sys

import "testing"

func TestBeginRunRestores(t *testing.T) {
	end, warn := BeginRun()
	if warn != "" {
		t.Logf("warning: %s", warn)
	}
	end()
	if throttleRefs != 0 {
		t.Fatalf("throttleRefs = %d after end", throttleRefs)
	}
}

package collector

import (
	"runtime"
	"testing"
)

func TestNumZombieProcsUsesSnapshot(t *testing.T) {
	originalGetSharedProcStates := getSharedProcStates
	defer func() {
		getSharedProcStates = originalGetSharedProcStates
	}()

	getSharedProcStates = func() (map[int32]string, bool) {
		return map[int32]string{1: "zombie", 2: "running", 3: "zombie"}, true
	}

	total, err := numZombieProcs()
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if total != 2 {
		t.Fatalf("expected zombie count 2, got %d", total)
	}
}

func TestNumZombieProcsFallsBackToGopsutil(t *testing.T) {
	originalGetSharedProcStates := getSharedProcStates
	originalCountZombieProcs := countZombieProcs
	defer func() {
		getSharedProcStates = originalGetSharedProcStates
		countZombieProcs = originalCountZombieProcs
	}()

	getSharedProcStates = func() (map[int32]string, bool) {
		return nil, false
	}
	countZombieProcs = func(goos string) (int, error) {
		if goos != runtime.GOOS {
			t.Fatalf("expected goos %s, got %s", runtime.GOOS, goos)
		}
		return 2, nil
	}

	total, err := numZombieProcs()
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if total != 2 {
		t.Fatalf("expected zombie count 2, got %d", total)
	}
}

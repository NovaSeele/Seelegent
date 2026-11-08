package platform

import "testing"

func TestMemoryReading(t *testing.T) {
	m := Memory()
	if m.AvailPhysMB < 0 || m.AvailCommitMB < 0 {
		t.Fatalf("negative reading: %+v", m)
	}
	if !m.Known && (m.AvailPhysMB != 0 || m.AvailCommitMB != 0) {
		t.Fatalf("an unknown reading must be all zero: %+v", m)
	}
}

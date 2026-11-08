package devicepairing

import (
	"errors"
	"testing"
)

func TestDeviceListIsGuardianScoped(t *testing.T) {
	s, f, _, _ := fixture(t)
	if _, err := s.ListDevices("intruder", "tenant-a", "child-a"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("intruder got list: %v", err)
	}
	if _, err := s.ListDevices("parent-a", "tenant-b", "child-a"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-tenant list allowed: %v", err)
	}
	devices, err := s.ListDevices("parent-b", "tenant-a", "child-a")
	if err != nil || len(devices) != 0 {
		t.Fatalf("unexpected list: %+v, %v", devices, err)
	}
	f.revoke("parent-b")
	if _, err := s.ListDevices("parent-b", "tenant-a", "child-a"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked guardian got list: %v", err)
	}
}

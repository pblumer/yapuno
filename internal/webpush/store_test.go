package webpush

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pblumer/yapuno/internal/devicepairing"
)

const (
	appleEndpoint  = "https://web.push.apple.com/QGuQyavXutnMH_Fd8Gu7dq3kYs7cV"
	googleEndpoint = "https://fcm.googleapis.com/fcm/send/dSf3WtUbhzk:APA91bG"
)

// fakeDevices approves the devices it lists.
type fakeDevices struct {
	mu     sync.Mutex
	active map[Device]bool
}

func newDevices(ds ...Device) *fakeDevices {
	f := &fakeDevices{active: map[Device]bool{}}
	for _, d := range ds {
		f.active[d] = true
	}
	return f
}

func (f *fakeDevices) ActiveDevice(tenant, child, device string) (devicepairing.Device, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.active[Device{tenant, child, device}] {
		return devicepairing.Device{ID: device, TenantID: tenant, ChildID: child}, true
	}
	return devicepairing.Device{}, false
}

func (f *fakeDevices) revoke(d Device) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.active, d)
}

var (
	alexPhone  = Device{TenantID: "tenant-a", ChildID: "child-a", DeviceID: "device-a1"}
	alexTablet = Device{TenantID: "tenant-a", ChildID: "child-a", DeviceID: "device-a2"}
	beaPhone   = Device{TenantID: "tenant-b", ChildID: "child-b", DeviceID: "device-b1"}
)

func openStore(t *testing.T, devices Devices) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "push.json")
	s, err := Open(path, devices)
	if err != nil {
		t.Fatal(err)
	}
	return s, path
}

func TestASubscriptionBelongsToAnApprovedDeviceAndSurvivesARestart(t *testing.T) {
	devices := newDevices(alexPhone, beaPhone)
	s, path := openStore(t, devices)
	if s.Subscribed(alexPhone) {
		t.Fatal("subscribed before subscribing")
	}
	if err := s.Subscribe(alexPhone, appleEndpoint); err != nil {
		t.Fatal(err)
	}
	if !s.Subscribed(alexPhone) || s.Subscribed(beaPhone) {
		t.Fatal("wrong subscription state")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("file mode %v %v", info.Mode(), err)
	}
	again, err := Open(path, devices)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.subscriptions("tenant-a", "child-a"); len(got) != 1 || got[0].Device != alexPhone || got[0].Endpoint != appleEndpoint {
		t.Fatalf("after restart: %+v", got)
	}
	// The file holds the endpoint and the device, nothing about messages.
	data, _ := os.ReadFile(path)
	for _, word := range []string{"p256dh", "auth", "body", "conversation"} {
		if strings.Contains(string(data), word) {
			t.Errorf("file mentions %q", word)
		}
	}
}

func TestOnlyApprovedDevicesAndKnownServicesCanSubscribe(t *testing.T) {
	devices := newDevices(alexPhone)
	s, _ := openStore(t, devices)
	if err := s.Subscribe(beaPhone, appleEndpoint); err != ErrForbidden {
		t.Fatalf("unapproved device: %v", err)
	}
	for _, d := range []Device{{}, {TenantID: "tenant-a", ChildID: "child-a"}, {TenantID: "tenant-a", DeviceID: "device-a1"}} {
		if err := s.Subscribe(d, appleEndpoint); err == nil {
			t.Fatalf("incomplete device %+v subscribed", d)
		}
	}
	if err := s.Subscribe(alexPhone, "https://attacker.example/x"); err != ErrInvalid {
		t.Fatalf("foreign host: %v", err)
	}
	if s.Subscribed(alexPhone) || s.Subscribed(beaPhone) {
		t.Fatal("refused subscription stored")
	}
}

func TestOneSubscriptionPerDeviceAndOneDevicePerEndpoint(t *testing.T) {
	devices := newDevices(alexPhone, alexTablet, beaPhone)
	s, _ := openStore(t, devices)
	if err := s.Subscribe(alexPhone, appleEndpoint); err != nil {
		t.Fatal(err)
	}
	if err := s.Subscribe(alexPhone, googleEndpoint); err != nil {
		t.Fatal(err)
	}
	if got := s.subscriptions("tenant-a", "child-a"); len(got) != 1 || got[0].Endpoint != googleEndpoint {
		t.Fatalf("replacement: %+v", got)
	}
	// The same endpoint handed in by another device moves; it is never
	// delivered to two devices, let alone to two children.
	if err := s.Subscribe(beaPhone, googleEndpoint); err != nil {
		t.Fatal(err)
	}
	if s.Subscribed(alexPhone) || !s.Subscribed(beaPhone) {
		t.Fatal("endpoint stored twice")
	}
	if err := s.Subscribe(alexTablet, appleEndpoint); err != nil {
		t.Fatal(err)
	}
	if err := s.Unsubscribe(alexTablet); err != nil {
		t.Fatal(err)
	}
	if s.Subscribed(alexTablet) || !s.Subscribed(beaPhone) {
		t.Fatal("unsubscribe touched the wrong device")
	}
	// Unsubscribing what is not there is not an error.
	if err := s.Unsubscribe(alexTablet); err != nil {
		t.Fatal(err)
	}
}

func TestStartupRemovesSubscriptionsOfDevicesNoLongerApproved(t *testing.T) {
	devices := newDevices(alexPhone, beaPhone)
	s, path := openStore(t, devices)
	for d, e := range map[Device]string{alexPhone: appleEndpoint, beaPhone: googleEndpoint} {
		if err := s.Subscribe(d, e); err != nil {
			t.Fatal(err)
		}
	}
	devices.revoke(alexPhone)
	if _, err := Open(path, devices); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), appleEndpoint) || !strings.Contains(string(data), googleEndpoint) {
		t.Fatalf("after restart: %s", data)
	}
}

func TestPurgeRemovesAChildOrAFamily(t *testing.T) {
	devices := newDevices(alexPhone, alexTablet, beaPhone)
	s, path := openStore(t, devices)
	for d, e := range map[Device]string{alexPhone: appleEndpoint, alexTablet: "https://web.push.apple.com/tablet", beaPhone: googleEndpoint} {
		if err := s.Subscribe(d, e); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.PurgeChild("tenant-a", "child-a"); err != nil {
		t.Fatal(err)
	}
	if s.Subscribed(alexPhone) || s.Subscribed(alexTablet) || !s.Subscribed(beaPhone) {
		t.Fatal("purge of a child")
	}
	if err := s.PurgeTenant("tenant-b"); err != nil {
		t.Fatal(err)
	}
	if err := s.PurgeTenant("tenant-b"); err != nil {
		t.Fatal("purging nothing failed")
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "tenant-") {
		t.Fatalf("left over: %s", data)
	}
}

func TestOpenFailsClosedOnAFileItDidNotWrite(t *testing.T) {
	devices := newDevices(alexPhone)
	dir := t.TempDir()
	for name, content := range map[string]string{
		"not JSON":        "nope",
		"unknown field":   `{"subscriptions":[],"extra":1}`,
		"trailing data":   `{"subscriptions":[]}{}`,
		"foreign host":    `{"subscriptions":[{"tenant_id":"tenant-a","child_id":"child-a","device_id":"device-a1","endpoint":"https://attacker.example/x"}]}`,
		"missing device":  `{"subscriptions":[{"tenant_id":"tenant-a","child_id":"child-a","endpoint":"` + appleEndpoint + `"}]}`,
		"device twice":    `{"subscriptions":[{"tenant_id":"tenant-a","child_id":"child-a","device_id":"device-a1","endpoint":"` + appleEndpoint + `"},{"tenant_id":"tenant-a","child_id":"child-a","device_id":"device-a1","endpoint":"` + googleEndpoint + `"}]}`,
		"endpoint twice":  `{"subscriptions":[{"tenant_id":"tenant-a","child_id":"child-a","device_id":"device-a1","endpoint":"` + appleEndpoint + `"},{"tenant_id":"tenant-a","child_id":"child-a","device_id":"device-a2","endpoint":"` + appleEndpoint + `"}]}`,
		"payload secrets": `{"subscriptions":[{"tenant_id":"tenant-a","child_id":"child-a","device_id":"device-a1","endpoint":"` + appleEndpoint + `","p256dh":"x"}]}`,
	} {
		path := filepath.Join(dir, strings.ReplaceAll(name, " ", "-")+".json")
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(path, devices); err == nil {
			t.Errorf("%s: opened", name)
		}
	}
	private := filepath.Join(dir, "shared.json")
	if err := os.WriteFile(private, []byte(`{"subscriptions":[]}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(private, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(private, devices); err == nil {
		t.Error("readable file opened")
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(private, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(link, devices); err == nil {
		t.Error("symbolic link opened")
	}
	if _, err := Open("", devices); err == nil {
		t.Error("empty path opened")
	}
	if _, err := Open(filepath.Join(dir, "new.json"), nil); err == nil {
		t.Error("opened without a device registry")
	}
}

func TestAFailedWriteChangesNothing(t *testing.T) {
	devices := newDevices(alexPhone)
	s, path := openStore(t, devices)
	s.failWrite = true
	if err := s.Subscribe(alexPhone, appleEndpoint); err != ErrUnavailable {
		t.Fatalf("failed write: %v", err)
	}
	if s.Subscribed(alexPhone) {
		t.Fatal("kept in memory after a failed write")
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), appleEndpoint) {
		t.Fatal("written")
	}
}

func TestAChildHasABoundedNumberOfSubscriptions(t *testing.T) {
	devices := newDevices()
	var all []Device
	for i := 0; i <= MaxPerChild; i++ {
		d := Device{TenantID: "tenant-a", ChildID: "child-a", DeviceID: "device-" + string(rune('a'+i))}
		devices.active[d] = true
		all = append(all, d)
	}
	s, _ := openStore(t, devices)
	for i, d := range all {
		err := s.Subscribe(d, "https://web.push.apple.com/"+d.DeviceID)
		if i < MaxPerChild && err != nil {
			t.Fatalf("subscription %d: %v", i, err)
		}
		if i == MaxPerChild && err != ErrFull {
			t.Fatalf("subscription beyond the limit: %v", err)
		}
	}
}

package webpush

import (
	"strings"
	"testing"
)

func TestOnlyEndpointsOfKnownPushServicesAreAccepted(t *testing.T) {
	for _, ok := range []string{
		"https://web.push.apple.com/QGuQyavXutnMH_Fd8Gu7dq3kYs7cV-ZL5rUPsQ8-XPaMUVZs",
		"https://fcm.googleapis.com/fcm/send/dSf3WtUbhzk:APA91bG_abc",
		"https://fcm.googleapis.com/wp/dSf3WtUbhzk:APA91bG_abc",
		"https://updates.push.services.mozilla.com/wpush/v2/gAAAAABk",
		"https://wns2-par02p.notify.windows.com/w/?token=BQYAAAB",
	} {
		if !ValidEndpoint(ok) {
			t.Errorf("rejected %s", ok)
		}
	}
	for _, bad := range []string{
		"",
		"http://fcm.googleapis.com/fcm/send/abc",
		"https://fcm.googleapis.com.evil.example/fcm/send/abc",
		"https://evilfcm.googleapis.com/fcm/send/abc",
		"https://push.apple.com.evil.example/abc",
		"https://notpush.apple.com/abc",
		"https://attacker.example/abc",
		"https://127.0.0.1/abc",
		"https://[::1]/abc",
		"https://localhost/abc",
		"https://user:pw@fcm.googleapis.com/fcm/send/abc",
		"https://fcm.googleapis.com:8443/fcm/send/abc",
		"https://fcm.googleapis.com:443/fcm/send/abc",
		"https://fcm.googleapis.com/fcm/send/abc?x=1",
		"https://fcm.googleapis.com/fcm/send/abc#x",
		"https://FCM.googleapis.com/fcm/send/abc",
		"https://fcm.googleapis.com",
		"https://fcm.googleapis.com/",
		"https://fcm.googleapis.com/fcm/send/a b",
		"https://fcm.googleapis.com/fcm/send/\n",
		"//fcm.googleapis.com/fcm/send/abc",
		"https://fcm.googleapis.com/" + strings.Repeat("a", 2048),
	} {
		if ValidEndpoint(bad) {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestWindowsEndpointsMayCarryTheirTokenInTheQuery(t *testing.T) {
	// WNS puts its token in the query; that is the only query accepted.
	if ValidEndpoint("https://fcm.googleapis.com/w/?token=BQYAAAB") {
		t.Fatal("query accepted for a service that does not use one")
	}
	if ValidEndpoint("https://wns2-par02p.notify.windows.com/w/?token=a&x=b") {
		t.Fatal("extra query parameter accepted")
	}
}

package guardianmailer

import (
	"context"
	"mime/quotedprintable"
	"strings"
)

// accessLockText is what another guardian of the same child reads. It names
// nobody, contains no address and deliberately no link: a security notice
// must not teach guardians to follow links from e-mail.
const accessLockText = `Hallo

Ein anderer Elternteil eurer Familie hat den eigenen Yapuno-Zugang gesperrt, weil ein Gerät verloren ging oder jemand Fremdes Zugriff gehabt haben könnte.

Was du jetzt tun kannst:
- Melde dich wie gewohnt bei Yapuno an. Diese Nachricht enthält absichtlich keinen Link.
- Prüfe bei euren Kindern die verbundenen Geräte und die freigegebenen Kontakte.
- Sperre Geräte, die du nicht kennst, unter «Gerät verloren?».

Dein eigener Zugang ist nicht betroffen. Den gesperrten Zugang kann nur der Betreiber dieser Yapuno-Installation wieder freigeben.

Kommt dir das unerwartet vor, sprich direkt mit dem anderen Elternteil.
`

func accessLockNotice(from, to string) string {
	var body strings.Builder
	w := quotedprintable.NewWriter(&body)
	_, _ = w.Write([]byte(accessLockText))
	_ = w.Close()
	return "From: " + from + "\r\nTo: " + to + "\r\nSubject: Yapuno: Zugang eines Elternteils gesperrt\r\n" +
		"Auto-Submitted: auto-generated\r\n" +
		"MIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n" +
		"Content-Transfer-Encoding: quoted-printable\r\n\r\n" + body.String()
}

// SendAccessLockNotice tells one verified guardian address that another
// guardian of the same child locked their own access.
func (s *Sender) SendAccessLockNotice(ctx context.Context, email string) error {
	return s.deliver(ctx, email, accessLockNotice(s.cfg.From, email))
}

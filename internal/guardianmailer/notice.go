package guardianmailer

import (
	"context"
	"errors"
	"fmt"
	"mime/quotedprintable"
	"strings"
	"unicode"
	"unicode/utf8"
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

// notice builds an automatic German text mail. The subject must be plain
// ASCII without line breaks; the text is quoted-printable encoded.
func notice(from, to, subject, text string) string {
	var body strings.Builder
	w := quotedprintable.NewWriter(&body)
	_, _ = w.Write([]byte(text))
	_ = w.Close()
	return "From: " + from + "\r\nTo: " + to + "\r\nSubject: " + subject + "\r\n" +
		"Auto-Submitted: auto-generated\r\n" +
		"MIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n" +
		"Content-Transfer-Encoding: quoted-printable\r\n\r\n" + body.String()
}

func accessLockNotice(from, to string) string {
	return notice(from, to, "Yapuno: Zugang eines Elternteils gesperrt", accessLockText)
}

// reportText tells a guardian that their own child reported a message. It
// names only that child: no message text, no other child, no link. The
// subject, which shows on lock screens, names nobody.
const reportText = `Hallo

%[1]s hat in Yapuno eine Nachricht gemeldet.

So siehst du die Meldung:
- Melde dich wie gewohnt bei Yapuno an. Diese Nachricht enthält absichtlich keinen Link.
- Öffne unter «Familie» die Kontakte von %[2]s. Dort steht die gemeldete Nachricht.

Sprich danach mit deinem Kind. Die andere Familie wurde nicht informiert.

Du bekommst höchstens eine solche E-Mail pro Stunde und Kind, auch wenn mehrere Nachrichten gemeldet werden.
`

// printableName returns the child's display name if it is safe to place in
// a mail body, and a neutral phrase otherwise.
func printableName(name string) (string, bool) {
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 40 {
		return "", false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", false
		}
	}
	return name, true
}

// reminderText is sent while no guardian has opened the page with the
// report. It follows the same rules as reportText.
const reminderText = `Hallo

%[1]s hat in Yapuno eine Nachricht gemeldet. Die Meldung wurde noch nicht angesehen.

So siehst du die Meldung:
- Melde dich wie gewohnt bei Yapuno an. Diese Nachricht enthält absichtlich keinen Link.
- Öffne unter «Familie» die Kontakte von %[2]s. Dort steht die gemeldete Nachricht.

Sobald ein Elternteil die Meldung angesehen hat, erinnert Yapuno nicht mehr. Es gibt höchstens zwei Erinnerungen pro Meldung.
`

// childWords returns how the texts refer to the child: by name, or neutrally
// when the name is not safe to print.
func childWords(childName string) (subject, object string) {
	if name, ok := printableName(childName); ok {
		return name, name
	}
	return "Dein Kind", "deinem Kind"
}

func reportNotice(from, to, childName string) string {
	subject, object := childWords(childName)
	return notice(from, to, "Yapuno: Eine Meldung wartet auf dich", fmt.Sprintf(reportText, subject, object))
}

func reportReminder(from, to, childName string) string {
	subject, object := childWords(childName)
	return notice(from, to, "Yapuno: Eine Meldung wartet noch auf dich", fmt.Sprintf(reminderText, subject, object))
}

// SendReportReminder tells one verified guardian address that a report of
// their own child has not been looked at yet.
func (s *Sender) SendReportReminder(ctx context.Context, email, childName string) error {
	return s.deliver(ctx, email, reportReminder(s.cfg.From, email, childName))
}

// SendReportNotice tells one verified guardian address that their own child
// reported a message.
func (s *Sender) SendReportNotice(ctx context.Context, email, childName string) error {
	return s.deliver(ctx, email, reportNotice(s.cfg.From, email, childName))
}

// SendAccessLockNotice tells one verified guardian address that another
// guardian of the same child locked their own access.
func (s *Sender) SendAccessLockNotice(ctx context.Context, email string) error {
	return s.deliver(ctx, email, accessLockNotice(s.cfg.From, email))
}

// registrationText asks an invited person to confirm their address. Unlike
// the security notices it must carry a link: following it is the proof of
// control over the mailbox. It names nobody.
const registrationText = `Hallo

Du wurdest zu Yapuno eingeladen und hast diese E-Mail-Adresse angegeben. Mit diesem Link bestätigst du die Adresse und legst deine Familie an:

%s

Der Link gilt 10 Minuten und nur einmal.

Hast du das nicht selbst angefordert, ignoriere diese Nachricht. Ohne den Link wird nichts angelegt.
`

func registrationMail(from, to, link string) (string, error) {
	// The server builds the link itself; still require plain HTTPS text.
	if !strings.HasPrefix(link, "https://") || strings.ContainsAny(link, " \t\r\n\x00<>\"") {
		return "", errors.New("invalid registration URL")
	}
	for _, r := range link {
		if r > unicode.MaxASCII || unicode.IsControl(r) {
			return "", errors.New("invalid registration URL")
		}
	}
	return notice(from, to, "Yapuno: Deine Einladung", fmt.Sprintf(registrationText, link)), nil
}

// SendRegistrationLink sends the confirmation link of an invited
// registration. Never print the link to logs.
func (s *Sender) SendRegistrationLink(ctx context.Context, email, link string) error {
	message, err := registrationMail(s.cfg.From, email, link)
	if err != nil {
		return err
	}
	return s.deliver(ctx, email, message)
}

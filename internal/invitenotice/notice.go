// Package invitenotice tells the guardians of a child by e-mail that an
// invitation of their family was used and connected that child with a child
// of the family that registered.
//
// The inviting guardian approved that contact when creating the invitation,
// before knowing who would register. The notice is what makes the result
// visible to them and to every other guardian who decides about the child's
// contacts. See docs/adr/draft-invitation-connects-children.md.
package invitenotice

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/pblumer/yapuno/internal/familydashboard"
)

// Guardians names the guardians of a child and their current permissions.
type Guardians interface {
	GuardiansOf(tenantID, childID string) []string
	Cards(guardianID, tenantID string) []familydashboard.ChildCard
}

// Addresses resolves the verified address of an active, unlocked guardian.
type Addresses interface {
	Email(ctx context.Context, guardianID, tenantID string) (string, bool)
}

// Mailer tells one guardian address that their own child has a new contact
// through an invitation. It must not be given anything but the address and
// the name of the guardian's own child.
type Mailer interface {
	SendInvitationContactNotice(ctx context.Context, email, childName string) error
}

// Notifier sends the notices in the background. A registration never waits
// for a mail server, and a mail that cannot be sent changes nothing else.
type Notifier struct {
	guardians Guardians
	addresses Addresses
	mailer    Mailer
	timeout   time.Duration
	wg        sync.WaitGroup
}

func New(guardians Guardians, addresses Addresses, mailer Mailer) (*Notifier, error) {
	if guardians == nil || addresses == nil || mailer == nil {
		return nil, errors.New("guardians, addresses and mailer are required")
	}
	return &Notifier{guardians: guardians, addresses: addresses, mailer: mailer, timeout: 15 * time.Second}, nil
}

// Connected is called after an invitation connected this child with a child
// of another family. Every guardian who currently decides about this child's
// contacts and has a usable address gets one mail that names only this child.
func (n *Notifier) Connected(tenantID, childID string) {
	if tenantID == "" || childID == "" {
		return
	}
	n.wg.Add(1)
	go func() {
		defer n.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), n.timeout)
		defer cancel()
		var sends sync.WaitGroup
		for _, guardian := range n.guardians.GuardiansOf(tenantID, childID) {
			name, ok := n.childFor(guardian, tenantID, childID)
			if !ok {
				continue
			}
			address, ok := n.addresses.Email(ctx, guardian, tenantID)
			if !ok || address == "" {
				continue
			}
			sends.Add(1)
			go func(address, name string) {
				defer sends.Done()
				if err := n.mailer.SendInvitationContactNotice(ctx, address, name); err != nil {
					// No address and no name in the log.
					log.Printf("invitation contact notice could not be sent")
				}
			}(address, name)
		}
		sends.Wait()
	}()
}

// childFor returns the child's name as this guardian sees it, if the
// guardian holds contact authority for exactly this child right now.
func (n *Notifier) childFor(guardian, tenantID, childID string) (string, bool) {
	for _, c := range n.guardians.Cards(guardian, tenantID) {
		if c.ID == childID && c.Permissions.ApproveContacts {
			return c.DisplayName, true
		}
	}
	return "", false
}

// Wait blocks until background sends have finished. It is for tests and for
// an orderly shutdown.
func (n *Notifier) Wait() { n.wg.Wait() }

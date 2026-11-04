// Package identity models guardian-controlled child device enrollment.
// It does not authenticate guardians or issue sessions; those require later work.
package identity

import (
 "errors"
 "time"
)

var (
 ErrNotAuthorized = errors.New("guardian not authorized")
 ErrExpired = errors.New("pairing challenge expired")
 ErrConsumed = errors.New("pairing challenge already consumed")
 ErrInvalid = errors.New("invalid pairing challenge")
)

// Challenge is persisted by a trusted store, not accepted from a client request.
type Challenge struct {
 ChildID string
 GuardianID string
 TokenDigest [32]byte
 ExpiresAt time.Time
 Consumed bool
}

// ApprovePairing checks the authoritative challenge and guardian-child relation.
// The caller must atomically mark the challenge consumed in persistent storage
// before provisioning a device. This pure function alone is not a security boundary.
func ApprovePairing(c Challenge, digest [32]byte, authenticatedGuardianID string, now time.Time, guardianMayManageChild bool) error {
 if !guardianMayManageChild || authenticatedGuardianID == "" || authenticatedGuardianID != c.GuardianID || c.ChildID == "" {
  return ErrNotAuthorized
 }
 if c.Consumed { return ErrConsumed }
 if !now.Before(c.ExpiresAt) { return ErrExpired }
 if c.TokenDigest == ([32]byte{}) || digest == ([32]byte{}) || c.TokenDigest != digest { return ErrInvalid }
 return nil
}

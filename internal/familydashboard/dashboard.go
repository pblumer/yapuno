// Package familydashboard builds least-privilege guardian dashboard views.
// Inputs must be loaded from trusted storage after authentication.
package familydashboard

import (
	"sort"

	"github.com/pblumer/yapuno/internal/authz"
)

// Child is a minimal projection. Never include message content in this view.
type Child struct {
	ID string
	TenantID string
	DisplayName string
}

// Permissions expose only actions authorized for the authenticated guardian.
type Permissions struct {
	ManageDevices bool
	ApproveContacts bool
}

// ChildCard is safe to render after the caller authenticates the guardian.
type ChildCard struct {
	ID string
	DisplayName string
	Permissions Permissions
}

// Build returns only children for whom the authenticated guardian has an
// active, scoped grant. Duplicate or conflicting grants fail closed: a
// permission is shown only when all matching grants allow it.
func Build(guardianID, tenantID string, children []Child, grants []authz.GuardianGrant) []ChildCard {
	if guardianID == "" || tenantID == "" {
		return nil
	}
	byChild := make(map[string][]authz.GuardianGrant)
	for _, grant := range grants {
		if grant.Active && grant.GuardianID == guardianID && grant.TenantID == tenantID && grant.ChildID != "" {
			byChild[grant.ChildID] = append(byChild[grant.ChildID], grant)
		}
	}
	seen := make(map[string]bool)
	out := make([]ChildCard, 0)
	for _, child := range children {
		if child.ID == "" || child.TenantID != tenantID || seen[child.ID] {
			continue
		}
		seen[child.ID] = true
		matches := byChild[child.ID]
		if len(matches) == 0 {
			continue
		}
		perms := Permissions{ManageDevices: true, ApproveContacts: true}
		for _, grant := range matches {
			perms.ManageDevices = perms.ManageDevices && authz.CanManageChildDevice(guardianID, child.ID, tenantID, grant)
			perms.ApproveContacts = perms.ApproveContacts && authz.CanApproveChildContacts(guardianID, child.ID, tenantID, grant)
		}
		out = append(out, ChildCard{ID: child.ID, DisplayName: child.DisplayName, Permissions: perms})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

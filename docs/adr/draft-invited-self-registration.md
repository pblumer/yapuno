# ADR-DRAFT: Invited self-registration of families with self-declared guardianship

- **Status:** Proposed
- **Implementation:** First slice on a branch, switched off by default: invitation store, public registration pages, guardian invite page, guardian-created child profiles (pending review)
- **Date:** 2026-11-21
- **Deciders:** maintainers; security, privacy and child-safety reviewers required

## Context and problem statement
Guardian identities are provisioned offline by the operator, who confirms with `-verified` that each identity was checked ([guardian sign-in ADR draft](draft-guardian-passwordless-email.md), [equal guardian provisioning](../planning/equal-guardian-provisioning.md)). The e-mail link proves control of a mailbox, not guardianship; the guardian directory is the place where "this adult is a guardian of this child" is asserted.

Setting up every test family through configuration files proved too slow for trials with synthetic profiles. The product owner asked for registration in the web application.

Any registration in the application moves the assertion of guardianship from the operator to the person who registers. That touches the invariants *bounded trust* and *mutual approval* ("no public directory, discoverability, anonymous contact or open invitations") and needs a recorded decision before it may be relied on.

## Decision drivers
- Trials must be possible without the operator editing files for each family.
- Nobody without a relationship to an existing family may register or cause e-mail to be sent.
- Registration must not become discovery: it must reveal nothing about existing families and connect no children.
- No statement in the UI may suggest that guardianship was checked.
- The default installation must behave exactly as before.
- Deny by default, durable before acknowledged, standard library only.

## Considered options
**Who may register**
1. Only with a one-time invitation created by a signed-in guardian (proposed).
2. Anyone with an e-mail address. Simplest for testers; anyone on the internet can create families, cause mail to arbitrary addresses and fill the stores. Rejected.
3. Anyone, activated by the operator afterwards. Keeps operator control; reintroduces the manual step that prompted this change.

**Where the feature applies**
1. Switchable, off by default (proposed).
2. Always on. Changes the trust model of every installation without review. Rejected.

**What a registered guardian may do**
1. Create their own family and child profiles; a second guardian for a child stays with the operator (proposed).
2. Also add a second guardian. Would let one adult grant another adult authority over a child without any check; deferred.

## Proposed decision outcome
Chosen options (product owner decisions of 2026-11-21): registration only through an invitation of an existing family; switchable and off by default; registered guardians create child profiles themselves.

### Rules
- The feature exists only while `YAPUNO_SIGNUP_FILE` is configured. Without it no route, link or form of this feature is served.
- A guardian with full authority for at least one child creates an invitation: a 128-bit one-time code, shown once, stored as a digest, valid 7 days. A guardian holds at most 3 open invitations and can withdraw them. At most 50 families can register on one installation. All numbers are placeholders.
- An invitation of a guardian who is no longer active or has locked their access is void.
- The invited person enters an e-mail address and the name of a first child and ticks a self-declaration of guardianship. A confirmation link (10 minutes, single use, kept in memory) is sent; only its confirmation creates anything.
- The answer to a registration request is the same whether the address is already registered, a limit was reached or a mail was sent. A registered address receives no mail. One address is asked at most once per minute and one invitation causes at most 5 requests that can send mail.
- Completion spends the invitation durably first, then creates a new tenant with one child and one guardian holding the baseline grant, then adds the identity to the guardian directory. A new guardian can never be attached to an existing tenant. Failures undo the earlier steps and reopen the invitation.
- The invitation record keeps identifiers only: who invited, and which tenant joined. It holds no address and no name. The inviting family is not told who registered, and the new family learns nothing about the inviting one.
- An invitation creates no contact. Children of the two families can talk only after the existing mutual approval ([contact invitations ADR draft](draft-contact-invitations.md)).
- A guardian with full authority creates further child profiles in their own tenant (at most 8 children per family). The creating guardian alone receives the grant. Ids are chosen by the server.
- The pages state that guardianship is not checked. The server logs at start that the feature is on.

### Consequences
- Positive: test families onboard themselves; no stranger can register or trigger mail; registration discloses nothing and connects nobody; the default installation is unchanged.
- Negative / accepted trade-offs:
  - **Guardianship is self-declared.** With the switch on, "guardian" means "an adult who was handed a link by another registered adult and controls a mailbox". Every protection that relies on guardians (contact approval, device management, reports) is then only as trustworthy as that chain. This is not sufficient for real children.
  - Trust is transitive: a registered family can invite further families. The chain is recorded but not limited in depth.
  - An invited person can enter someone else's address, who then receives one unexpected mail; the limits bound the volume, not the possibility.
  - A crash between the steps of completion can leave a family record nobody can sign in to and a spent invitation. The inviter creates a new invitation; the orphan needs operator cleanup.
  - The application now writes the guardian directory, which was operator-only. Offline provisioning must stop the server first (as before for the family store).
  - There is no self-service to delete a family, remove a child or change the address.
- Open questions and review gates:
  - **Verification of guardianship** for real children: which evidence, who checks it, and whether invited registration may then only create an *unverified* family with reduced abilities until checked.
  - Whether contact approval between two unverified families is acceptable, and what children and guardians are told about it.
  - Second guardians: invitation by the first guardian, with which safeguards against coercion and lock-out.
  - Operator view of the invitation chain, removal of abusive families, and deletion on request.
  - Limits (invitations, families, children, mail) and whether a CAPTCHA-free design holds under real abuse.
  - Legal assessment of consent and age rules for the target countries.

## Links
- Issues [#2](https://github.com/pblumer/yapuno/issues/2), [#3](https://github.com/pblumer/yapuno/issues/3), [#8](https://github.com/pblumer/yapuno/issues/8)
- [Invited self-registration](../planning/invited-self-registration.md), [trial run](../planning/trial-run.md)
- [Guardian sign-in ADR draft](draft-guardian-passwordless-email.md), [guardian contact trust ADR draft](draft-guardian-contact-trust.md), [guardian access lock ADR draft](draft-guardian-access-lock.md)

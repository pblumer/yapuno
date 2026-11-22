# Invited self-registration

**Status:** engineering preview for synthetic families, **switched off by default**. A signed-in guardian invites another family, which registers itself and creates its child profiles. Decision record: [ADR draft: invited self-registration](../adr/draft-invited-self-registration.md).

> With this switch on, Yapuno does **not** check that a registering person is a guardian. They declare it themselves. Do not use it with real children.

## Enable

```sh
export YAPUNO_SIGNUP_FILE=private/signup.json
```

The file is created empty with mode `0600` at start. The mailer must be able to send registration links (the built-in SMTP mailer can). The server logs at start that self-registration is on. Without the variable none of the pages below exist.

The first family still comes from [offline provisioning](trial-run.md#provisioning-the-families): somebody has to send the first invitation.

## Flow

1. A guardian opens **Familie → Eine Familie einladen** (`/family/invite`) and creates an invitation link. The link is shown once.
2. The guardian passes the link to another adult they know personally.
3. That person opens the link (`/join?code=…`), enters an e-mail address and the first child's name, and confirms being the child's guardian.
4. Yapuno sends a confirmation link (valid 10 minutes, single use). Opening it shows a page with one button; pressing it creates the family and signs the guardian in.
5. The new guardian connects a device under **Geräte von …**, adds further children under **Weiteres Kind anlegen** (`/family/children`) and can invite families as well.

From then on the guardian signs in at `/login` like every other guardian.

## What it does and does not do

- An invitation is valid 7 days and once. A guardian holds at most 3 open invitations and can withdraw each. At most 50 families can register per installation; a family has at most 8 children.
- An invitation of a guardian who locked their access or was deactivated is void.
- The inviting family does not learn who registered; the new family learns nothing about the inviting one. **The children are not contacts.** That still needs the approval of both families.
- A registered address gets no registration mail, and the page answers the same in every case. One address is asked at most once per minute; one invitation causes at most 5 requests that can send mail.
- A new child profile is granted to the guardian who created it. A second guardian for a child is still added by the operator with `yapuno-provision -add`.
- Unconfirmed registrations live in memory: after a restart the confirmation link is invalid and the invitation can be used again.

## Operator notes

- `signup.json` holds identifiers only: per invitation the inviting guardian and tenant, times, a counter, and after use the tenant that joined. Used invitations stay as the record of who invited whom. It contains no address, no name and no usable code.
- The application now writes `guardians.json`. **Stop the server before running `yapuno-provision -add`**, as before.
- To stop a registered guardian, set `"active": false` on their entry in `guardians.json`; it applies at once, also to their open invitations.
- There is no self-service to delete a family or a child. Removing one means editing the files while the server is stopped.
- Put the reverse proxy's rate limit on `POST /join/request` as well as on `POST /login/request`.

## Known limits

- Guardianship is self-declared; see the ADR draft for what that means.
- Somebody who holds an invitation can enter another person's address, who then receives one unexpected mail.
- If the server stops between spending the invitation and storing the identity, the invitation is spent and a family record without a sign-in remains. The inviter creates a new invitation.
- Verified in a real desktop browser at 320, 375 and 430 px; not yet on real phones.

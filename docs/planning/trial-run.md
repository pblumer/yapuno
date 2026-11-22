# Trial run with two synthetic families

**Purpose:** play the "First Hello" path once on real phones, with synthetic profiles and people who know it is a test. This is how the real-device gaps listed in the planning documents get their first evidence. It is **not** a pilot and gives no permission to use Yapuno with real children's data.

## What you need

- A deployment of `cmd/yapuno-web` behind HTTPS with working SMTP ([setup](guardian-browser-login-runtime.md)).
- Two families made of synthetic profiles, for example:

  | Family | Guardian | Child |
  |---|---|---|
  | `family-a` | `anna` | `alex` ("Alex") |
  | `family-b` | `bruno` | `bea` ("Bea") |

  Optionally a second guardian for `alex` (to see the lock notice and two recipients of a report) and a second child (to see that siblings are separate).
- **One mailbox per guardian that the tester can read.** Two guardians cannot share an address.
- Two phones for the children, ideally one iPhone and one Android phone, and a device per guardian.

## Configuration

All state lives in one private directory (mode `0700`). The two required files are created by provisioning; the optional ones are created by the server.

| Variable | Enables | Needs |
|---|---|---|
| `YAPUNO_FAMILIES_FILE`, `YAPUNO_GUARDIANS_FILE` | guardian login and family page (required) | provisioning |
| `YAPUNO_PAIRING_FILE` | device pairing, child sign-in, "Gerät verloren" | |
| `YAPUNO_CONTACTS_FILE` | contact approval between families | |
| `YAPUNO_MESSAGES_FILE` | chat, blocking | pairing and contacts |
| `YAPUNO_APPEARANCE_FILE` | "Mein Look" (themes and figures) | |
| `YAPUNO_REPORTS_FILE` | reporting, e-mail notice, reminders | messages |
| `YAPUNO_GUARDIAN_LOCKS_FILE` | emergency lock of a guardian's access | |
| `YAPUNO_GUARDIAN_CHAT_FILE` | guardians chat with their own children and with guardians of connected families ([details](guardian-chat.md)) | |
| `YAPUNO_SIGNUP_FILE` | optional: families register through an invitation and create child profiles ([details](invited-self-registration.md)); guardianship is then self-declared | |

For the trial set all of them. `YAPUNO_SIGNUP_FILE` is the exception: set it only if further families should register themselves instead of being provisioned.

## Provisioning the families

The first family is created once, while no files exist:

```sh
yapuno-provision -verified -families private/families.json -guardians private/guardians.json \
  -tenant family-a -guardian anna -email anna@example.org -child alex -name Alex
```

Everything else is added with `-add`. **Stop the server first**: it keeps the family store in memory and would neither see nor preserve the addition.

```sh
# a second family
yapuno-provision -verified -add -families private/families.json -guardians private/guardians.json \
  -tenant family-b -guardian bruno -email bruno@example.org -child bea -name Bea
# optional: a second, equal guardian for Alex
yapuno-provision -verified -add -families private/families.json -guardians private/guardians.json \
  -tenant family-a -guardian albert -email albert@example.org -child alex -name Alex
# optional: a sibling
yapuno-provision -verified -add -families private/families.json -guardians private/guardians.json \
  -tenant family-a -guardian anna -email anna@example.org -child ada -name Ada
```

`-add` never changes or removes what exists and prints `changed: …` or `unchanged`, so it can be repeated; `-add -dry-run` prints `would change: …` without writing. It refuses an address that already belongs to another guardian, an existing guardian given another address, and files or directories that are not private. A child that exists keeps its current name. `-verified` remains the operator's confirmation that identities were checked and the profiles are synthetic.

### Alternative: let further families register themselves

With `YAPUNO_SIGNUP_FILE` only the first family needs provisioning. Its guardian opens **Familie → Eine Familie einladen**, creates a link and passes it on; the invited person registers with their own address and the first child's name, and adds further children under **Weiteres Kind anlegen**. A second guardian for a child still needs `-add`. See [invited self-registration](invited-self-registration.md) for the limits, above all that guardianship is not checked.

## Test script

Note for each step: device, operating system and browser, what happened, and anything that was unclear. A step is only passed if the stated result appeared without help.

### A. Guardians and devices
1. Each guardian requests a sign-in link, receives it and signs in. *Note how long the e-mail took and whether it landed in spam.*
2. Each guardian sees only their own child on **Familie**.
3. Each guardian chooses a theme and figure under **Mein Look**.
4. Each guardian opens **Geräte von …** on the child's card, creates a pairing code, and the child's phone scans the QR code with its normal camera. The fingerprints match, the guardian approves, the child lands on **Hoi …!**.
5. The child adds the page to the home screen and opens it from there. *Is the child still signed in? After closing and reopening? After an hour? The next day?*

### B. Contact and first hello
6. Family A creates an invitation code and passes it to family B (read aloud). Family B redeems it; family A confirms. Both children see each other under **Deine Crew**.
7. Negative: before step 6 is complete, neither child sees the other.
8. Both children write. Messages show **Gesendet** with a time and arrive on the other phone within a few seconds.
9. One phone goes into flight mode, writes a message, and comes back. The message shows **Nicht gesendet** with **Nochmal senden**; after retrying it arrives exactly once.
10. Lock the phone for a few minutes, then open the chat again. *Do new messages appear? Is a new sign-in needed?*
11. Each child changes theme and figure; the other child sees the new figure, not the theme.

### B2. Guardians write too
11a. A guardian opens **Mit … schreiben** on the child's card and writes. The child finds the message under **Deine Eltern** and answers; the answer appears on the guardian's page without reloading.
11b. Each guardian opens **Chats** and finds the other family as **Elternteil von …**. Both write; messages arrive without reloading.
11c. Negative: under **Chats** a guardian finds no conversation between the children, and a child finds no conversation between the guardians.

### C. Something goes wrong
12. A child opens **Melden**, reports one message and reads the text on the page. *Ask the child afterwards what they think the parents can now see.*
13. The guardians receive an e-mail that names only their child. *Spam? Understandable without explanation?* On **Kontakte von …** they see exactly that message and nothing else of the chat. The other family sees nothing.
14. The child opens **Melden** again and sees **Deine Eltern haben die Meldung gesehen**.
15. Leave a second report unopened for more than three hours: a reminder arrives.
16. A child blocks the other. The other child's chat ends within seconds with a neutral text; their start page shows no hint. The other family sees **Pausierte Kontakte**. Only the blocking child's guardian can lift it.
17. A guardian uses **Gerät von … verloren?** to lock the child's phone: the open chat closes within seconds. Pairing the phone again works and the conversation is back.

### D. Guardian safety
18. **Abmelden**, then **Überall abmelden** with a second browser signed in.
19. Only with a second guardian or after agreeing who restores access: **Meinen Zugang sperren**. No sign-in link arrives afterwards; the other guardian receives a notice. Restore by removing the entry from the lock file ([how](guardian-account-safety.md#lifting-a-lock-operator)).

### E. Look and feel on the phone
20. Portrait and landscape, with the keyboard open, with the system font size increased, and with the system set to dark mode and to reduced motion: nothing overlaps, nothing is cut off, every button can be tapped.
21. With VoiceOver or TalkBack: can a message be read and sent, and can **Melden** be found?

## What to bring back

- Which steps failed or needed help, with device and browser.
- Delivery time and spam classification of each kind of e-mail.
- What children and guardians said they expected at steps 12 to 16.
- How long a child stayed signed in on each phone (step 5 and 10).

These observations decide what is built next; they replace none of the reviews listed under pilot readiness in the [roadmap](../../ROADMAP.md).

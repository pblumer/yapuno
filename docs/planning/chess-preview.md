# Chess between approved contacts — browser preview

**Status:** engineering preview for synthetic child profiles. Two children whose families approved their contact, and whose guardians each allowed chess, can play chess from their paired devices, live with a clock or by correspondence. **It is not safe for real children:** no playtime quota, no notifications, no real-device or screen-reader evidence. Decision record: [ADR draft: chess](../adr/draft-yapuno-play-chess.md). Issue [#19](https://github.com/pblumer/yapuno/issues/19).

## Enable

```sh
export YAPUNO_PAIRING_FILE=private/pairing.json
export YAPUNO_CONTACTS_FILE=private/contacts.json
export YAPUNO_PLAY_FILE=private/play.json
export YAPUNO_GAMES_FILE=private/games.log
go run ./cmd/yapuno-web
```

Practice against the computer additionally needs `export YAPUNO_PRACTICE_FILE=private/practice.json`, the private rating `export YAPUNO_RATINGS_FILE=private/ratings.json`; both require the two chess files. Games between a guardian and their own child are on when the guardians' chat (`YAPUNO_GUARDIAN_CHAT_FILE`) is on as well, because that is where a child sees its guardians.

The play and games files are needed together and require pairing and contacts; the server refuses to start otherwise. Both files are created with mode `0600`. Without them, no chess route or link exists and the family overview lists "Schach" as inactive.

## Flow

1. A guardian opens **Spiele von …** on the family overview and allows chess for their child. It is off by default. The guardians of the other child must do the same for their child.
2. On the child's home page a knight appears next to each contact the child may play with, marked **Einladung** or **Du bist am Zug** when something is waiting.
3. The child chooses a mode (correspondence, or live with 5, 10 or 20 minutes plus an increment) and invites. The server chooses colours at random.
4. The other child accepts or declines. Moves are made by tapping a piece and then one of the highlighted target squares, or by typing a move such as `e2e4`. A pawn reaching the last rank asks for the piece.
5. Draw offers, resigning (behind a confirmation) and aborting before both first moves are available. The page reloads when the other child acts; in a live game it checks every second while the opponent is to move.

## Tips and practice

- **Tips in a game between children** are chosen at the invitation ("Mit Tipps") and visible to both before accepting. Each child may take **three tips per game**. A tip first explains the position (mate in one possible, own king in check, a threatened mate, an own piece in danger, a piece to win, develop, castle, or a general prompt); "Zeig mir einen Zug" on the same move then names a move and marks its two squares in green. Only the taker sees the content; the opponent sees "Mia hat 1 genutzt". The tip is computed once and stored with the action.
- **Practice against the computer** (`/child/practice`): three levels (easy plays at one half-move with deliberate randomness, medium at two, hard at three half-moves plus captures), choice of colour, unlimited tips, taking back the last own move, resigning. No clock and no polling: the computer answers within the same request.
- **These are heuristics for children, not engine analysis.** Attackers and defenders are counted without exchanges, pins or x-rays, and the search looks only a few half-moves ahead with a simple evaluation. In the headless check, the move tip after 1.e4 e5 2.Nf3 Nc6 was "Läufer f1 nach d3": legal and not losing, but a move a chess teacher would not recommend, because it blocks the d-pawn. The texts therefore say "ein guter Zug", not "der beste Zug". A better evaluation is possible later; it needs tests against positions a trainer has judged.
- Cost: the hardest search took at most about 60 ms in complex middlegame positions on the development container; the node limit bounds it. A child may make at most 30 practice moves or tip requests per minute.

## Playing with the own guardians

- Under **Deine Eltern** a knight next to each guardian leads to the family game; on the family overview **Schach mit …** leads to it from the guardian's side. Either side may invite; tips work as between children.
- Authorization: `authz.CanPlayFamilyGame` — a current grant for exactly this child (no management capability needed), the guardian identity active and not locked, the child profile, the child's approved device when the child acts, and chess allowed for the child. Guardians of other families cannot reach the game, also not with a guessed path; a guardian session gets `401` on child routes.
- A family game is identified by a derived pair id of guardian and child; the page carries no other identifier.

## Private rating

- Each child sees **its own** rating on its home page, with "vorläufig" for the first ten rated games and the sentence "Nur du siehst diese Zahl. Sie zeigt, wie du dich verbesserst, nicht wer besser ist." Nobody else sees it: not other children, not guardians, no list. Guardians always count as a fixed 1000; nothing is stored about them. The rating falls after a loss, on purpose.
- Elo, K = 40, start 1000. Rated: results by the rules, resignation, time-out, agreed draw. Not rated: declined, withdrawn, expired, aborted, abandoned games, and practice games with a tip or a takeback.
- **The computer's ratings (easy 600, medium 900, hard 1200) are uncalibrated assumptions.** The number shows a child's own progress; it is not comparable with a club or FIDE rating.
- Each game is rated once by its key, also time-outs, when the ended game is next read or compacted; ratings outlive the deletion of games and are deleted with the child's profile or the family.

## Rules and outcomes

- The server's rules engine ([`internal/chess`](../../internal/chess)) decides legality; perft counts of seven published reference positions match. The page offers only moves the server listed as legal, and the server checks every move again.
- Checkmate, stalemate, insufficient material, threefold repetition and the fifty-move rule end the game. **For children, repetition and fifty-move draws apply automatically** instead of by claim.
- A king in check is framed in red and announced ("steht im Schach"); the status line says "Dein König steht im Schach!"; the last move is described in words ("Mia: Läufer f1 nach b5, Schach").
- Live clocks: each side's first move is untimed; the clock is computed from server timestamps. A time-out against a side that cannot mate is a draw.
- Invitations lapse after 7 days; a game without action for 30 days ends without result; ended games are deleted 30 days after their end (provisional, #7).

## Security properties (tested)

- **Every invitation, action and read is authorized from current state** with `authz.CanPlayGame`: both profiles, the device in use, an active, mutually approved and unblocked contact, and chess allowed for **both** children. Denied alike: outsiders with their own approved device, siblings, forged parties, unknown contacts or games, revoked devices. Switching chess off, blocking, ending the contact, locking the device or removing a profile stops the game on the next request.
- The game in a request must belong to the contact in the path before anything is done.
- **One unfinished game per contact**; at most 5 invitations per child and contact in 24 hours; at most 3 draw offers per player and game. There is no free text in games.
- **Durable before acknowledged, idempotent:** an action is acknowledged only after it was appended and synced; a retry with the same client id returns the original result, a different action with the same id is a conflict; a stale half-move count is refused.
- **Fail-closed log:** every game is replayed through the rules engine on start; an illegal stored move, a sequence gap, an unknown field or an insecure file prevents start.
- Ending a contact deletes its games from disk; deleting a child profile deletes its game settings.
- Guardians have no route into a game; their session gets `401`.
- Mutation checks: 28 store mutations, 6 web-handler mutations, 4 guardian-page mutations and 10 rules-engine mutations are each detected by the tests.

## Browser evidence (headless Chromium, not a physical device)

Page rendered at 320 and 390 CSS px: no horizontal overflow; tap on a bishop highlights exactly its five legal targets and announces them; tapping a target posts the expected move with the expected half-move count; no script errors. Squares measure **40 px at 320 px** (below the MFE target of 44 px, documented exception in the ADR) and 48 px at 390 px. A CSS specificity bug (dark squares drawn white) was found this way and fixed.

**Not verified:** physical iPhone/Android, rendering of the chess glyphs on those systems, VoiceOver/TalkBack, large text, landscape, background and resume, poor networks.

## Limitations

- No playtime quota (#19 leaves the semantics open).
- Tips and the computer opponent are simple heuristics (see above); the rating is private and not calibrated; there are no rankings.
- No notifications; a child sees an invitation or a move only when opening Yapuno.
- Polling, not realtime; single server process; all games in memory.
- Games between siblings, with guardians of other families or in groups are not possible. Family games need the guardians' chat to be on.

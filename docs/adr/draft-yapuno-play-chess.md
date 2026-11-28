# ADR-DRAFT: Chess between approved contacts, played live or by correspondence

- **Status:** Proposed
- **Implementation:** First slice on a branch (pending review): rules engine, guardian switch, game store, child chess page and guardian games page as a synthetic browser preview, with tips, practice against the computer, games with the own guardians, a private rating and a review after the game; see [chess preview](../planning/chess-preview.md). No quota, no notifications, no real-device evidence.
- **Date:** 2026-11-28
- **Deciders:** maintainers; child-safety, privacy and accessibility review required before real use

## Context and problem statement
Issue [#19](https://github.com/pblumer/yapuno/issues/19) (Yapuno Play) asks for age-appropriate games that children play with contacts their families already approved, under guardian control, with chess as the first multiplayer game. A pure quota predicate exists in `internal/play`; there is no game session, no rules engine, no game storage and no game UI.

Children are rarely online at the same moment, but a child who likes chess also wants to play a real game with a friend sitting at another device. Both forms are wanted:

- **Correspondence ("Fernschach"):** each child moves when it suits; a game may last days.
- **Live:** both children are present; each side has a chess clock.

The existing building blocks fit a turn-based game closely: tenant-qualified contacts with mutual guardian approval and child-side blocking, key-bound child device sessions, a durable append-only log with server sequence numbers and idempotent retries ([message storage](draft-message-storage.md)), and authenticated polling as the transport.

## Decision drivers
- Deny by default at the moment of every action (invariants 1 and 2): invitations, moves, reads and polls are authorized from current contact, block, profile, device and guardian-enablement state.
- A game must never create a channel that a contact, block or guardian decision does not allow (invariant 3). No public matchmaking, no strangers, no free-text in games.
- Server-authoritative rules and clocks: a client may propose a move, never decide its legality, the result or the time left.
- Durable before acknowledged; retries must not play a move twice (invariant 8).
- Mobile First Experience (invariant 6): small screens, touch, screen readers, unreliable connections.
- Standard library only; no realtime dependency decided inside a feature slice.
- Gaming must not affect messaging, and messaging rules must not be weakened by gaming.

## Considered options
### Rules engine
1. **Own rules engine in Go, verified by perft** (proposed). The rules of chess are fixed and fully specified; perft counts of legal move trees for published reference positions give an objective, exhaustive test.
2. **Third-party Go chess library.** Saves work, but adds a dependency whose maintenance status would need a lifecycle review, and the server would still need its own result, draw and clock policy.
3. **Rules in the browser only.** Rejected: the server would have to trust the client.

### Transport
1. **Authenticated polling of a state endpoint with a version cursor** (proposed), faster while the opponent is to move in a live game.
2. **Server-Sent Events or WebSocket.** Better latency, but the realtime transport is explicitly undecided for messaging; deciding it here would decide it for the whole product.

### Clock
1. **Clock computed from server timestamps of recorded actions; time-out established lazily** (proposed). The time a side used is the difference between server times of consecutive recorded actions. The server needs no timer: whenever a game is read or acted on, a side whose time has run out has lost on time.
2. **Server-side timers per game.** More moving parts (scheduling, restarts) for no observable difference, since nobody can act on a game without a request.
3. **Client-reported clock.** Rejected.

### Guardian control
1. **Each child's own guardians enable chess for their child; both children of a game must be enabled** (proposed). Off by default.
2. **Chess allowed for every approved contact.** Simpler, but a guardian who approved a chat contact did not approve games, and issue #19 requires per-child enablement.

## Proposed decision outcome
Chosen options (if approved): **own rules engine, polling, clock from server timestamps, per-child guardian enablement**.

### Who may play
- A game belongs to exactly one contact (relationship id) and to its two children. A new `authz.CanPlayGame` predicate requires everything `authz.CanSendMessage` requires (both profiles exist, device approved, contact active and mutually approved, not blocked) **and** that the guardians of each child have enabled chess for that child.
- Enabling requires a guardian whose current grant for the child includes contact approval, since a game is a form of contact. Any such guardian may switch it on or off; the most recent decision applies. Switching off, blocking, removing the contact, locking the device or removing a profile stops invitations, moves and reads on the next request.
- Unknown games, games of other children and games whose contact no longer allows play all get the same "not allowed" answer.
- There is at most **one unfinished game per contact** (invited or running). This bounds what one child can send to another without a rate limit of its own.
- Children who share a family have no contact and cannot play each other in this slice.

### Game lifecycle
- **Invitation:** a child invites a contact and chooses a mode: correspondence, or live with one of three fixed time controls (5 min + 3 s, 10 min + 5 s, 20 min + 10 s per move). The inviter's colour is chosen by the server at random. An invitation expires after **7 days**; the inviter may withdraw it; the invited child may decline or accept.
- **Moves:** a move is sent in UCI notation (for example `e2e4`, `e7e8q`) with the number of half-moves the client has seen and a random client move id. The server applies it only if the game is running, it is the child's turn, the half-move count matches the stored game and the move is legal. `(game, player, client move id)` is unique: a retry with the same move returns the original result and writes nothing; the same id with a different move is a conflict. A stale half-move count is reported as such so the client reloads.
- **Ends:** checkmate, stalemate, resignation, agreed draw, time-out, and draws by insufficient material, threefold repetition and the fifty-move rule.
- **Deviation from FIDE rules, deliberately for children:** repetition and fifty-move draws are applied **automatically** rather than claimed. A time-out when the opponent cannot possibly mate (insufficient material) is a draw, as in FIDE rules; the material test is the standard insufficient-material test, which is narrower than "no possible mate".
- **Draw offers:** a player may offer a draw on a running game; the opponent may accept or decline. A move by the opponent declines it. At most one offer is pending.
- **Abandonment:** a correspondence game without a move for **30 days** ends without a result. A live game whose clock ran out ends on time; a live game is never abandoned in another way.

### Clock rules (live games)
- Each side's first move is untimed, so a live game does not start losing time while the other child is still finding the page. The clock of the side to move runs from the server time of the previous move.
- After a move, the mover's remaining time is reduced by the time used and increased by the increment.
- A side whose remaining time is zero or less at the server's current time has lost on time (draw if the opponent cannot mate). The end is derived from the recorded server times whenever the game is read or acted on; because the moment the flag fell follows from the log alone, nothing is written for it. The same holds for expired invitations and abandoned games. A move that arrives after the flag fell is refused.
- Clocks shown in the browser are a projection of the server state; they are never sent back.

### Storage and durability
- One append-only JSON-lines log with `fsync` before acknowledgement, the same pattern as the message log: per-game sequence numbers, private file (`0600`), fail-closed load, truncation of a partial final line, atomic rewrite at compaction.
- The log is the source of truth: on start every game is replayed through the rules engine, and an illegal stored move prevents start.
- Kept data is minimal: game id, contact id, both children, mode, colours, actions with server time and client move id. No chat, no free text, no device identifiers in the game.
- **Retention (provisional, issue #7):** finished, declined, withdrawn and expired games are deleted **30 days** after they ended; games of a contact that no longer exists are deleted at the next compaction.

### Transport
- Authenticated HTTP. `GET` returns the current state and a version (the per-game sequence number); `POST` performs an action and returns the new state. The scripted page polls while visible: about every second in a live game while the opponent is to move, otherwise every few seconds with back-off on errors.
- The state response carries no identifiers of either child or device: only "you" and the contact's display name, colours, moves, clocks and status.

### Interface (Mobile First Experience)
- The board is server-rendered. Without JavaScript a child enters a move as text and submits a form; the page works fully. With JavaScript, a child taps a piece, sees its legal target squares highlighted and taps the target; a pawn reaching the last rank asks for the piece.
- Every square is a button with an accessible name such as "e4, weisser Bauer"; legal targets and the last move are announced in words, not only in colour.
- **Touch target exception:** the MFE spec asks for 44 × 44 CSS px. An 8 × 8 board at a viewport width of 320 CSS px allows at most 40 px per square even edge to edge. The board therefore spans the full width below 352 px, squares meet 44 px from a 352 px viewport, and the two-step tap with visible selection makes a missed tap harmless (nothing is played until a highlighted target is tapped). This exception needs real-device evidence.
- Pieces are drawn with the chess characters of the system font, always in their solid form and coloured by the skin; this avoids image assets and licence questions. Rendering on real iOS and Android devices must be verified; own SVG pieces are the fallback.

### Tips (help mode)
- Whether a game between two children has tips is chosen **at the invitation** and shown to both before they accept. Each player may take at most **three tips per game**; both players see how many tips each has used. This keeps a game with tips fair without a guardian decision per game.
- A tip is requested only by the player to move. **Level 1 explains** the most important thing in the position, in this order: a mate in one is possible; the own king is in check; the opponent threatens mate in one; an own piece is in danger (attacked and undefended, or attacked by a cheaper piece); a piece can be won (an undefended piece, or one worth more than the capturing piece); in the opening, develop knights and bishops or castle; otherwise a general prompt. **Level 2** on the same move names a good move found by a short search. Level 2 counts as part of the same tip.
- The tip is computed once, when it is requested, and stored with the action, so a reload shows the same tip and costs no new search. Only the player who took a tip sees its content.
- The analysis is a **heuristic for children, not an engine evaluation**: it counts attackers and defenders without exchanges, x-rays or pins, and the search looks only a few half-moves ahead. A tip can be wrong; the texts say "ein guter Zug", not "der beste Zug".

### Practice against the computer
- A child may play against a computer opponent at three levels (easy: one half-move with deliberate randomness; medium: two half-moves; hard: three half-moves plus captures), choose white, black or random, take back the last own move and ask for tips without limit. There is no clock.
- Practice involves no other child, so it needs no contact. It requires an authenticated child session on an approved device and that a guardian has allowed chess for that child; switching chess off stops practice too.
- One practice game per child is kept in a separate private file, replaced when a new one starts and deleted with the child's profile. A move is acknowledged only after the child's move and the computer's reply are written and synced together.
- The search is bounded by a node limit, and a child may make at most 30 practice moves or tip requests per minute, so a scripted client cannot keep the server busy.

### Games between a child and its own guardian
- A child may play a guardian who currently holds a grant for exactly this child (no management capability needed, as for the family chat), and that guardian may play the child. Either side may invite. Guardians of other families cannot play a child.
- Authorization uses a new `authz.CanPlayFamilyGame`: everything `authz.CanUseFamilyChat` requires (current grant, guardian identity active and not locked, child profile exists, the child's device approved when the child acts) **and** chess allowed for the child. Removing the grant, locking the guardian, locking the device or switching chess off stops the game on the next request.
- Family games use the same store, rules, clocks, tips and limits as games between children. A family game is identified by a derived pair id of guardian and child; it carries no other identifiers to the page. The child reaches it next to the guardian under "Deine Eltern", so it needs the guardians' chat, which is where the child sees its guardians; the guardian reaches it from the family overview.
- Guardians are adults: no quota applies to them, and tips follow the same rule for both sides.

### Private rating
- Each child has a rating that **only the child sees**, on its own home page. It is not shown to other children, to guardians, or in any list. There is no ranking and no comparison.
- Elo with K = 40 (the FIDE factor for juniors), starting at 1000, marked "vorläufig" until 10 rated games. Rated are games that ended with a result by the rules, resignation, time-out or agreed draw; declined, withdrawn, expired, aborted and abandoned games are not.
- Opponents: other children; guardians, who always count as a fixed 1000 so that **no rating of an adult is stored** (data minimisation; a guardian's real strength is ignored, which makes the child's rating less precise in family games); and the computer at fixed, **uncalibrated** ratings (easy 600, medium 900, hard 1200). These anchors are assumptions; the number is meaningful for the child's own progress, not as a comparison with any official rating. A practice game counts only if no tip and no takeback was used. A rating falls after a loss; that is intended.
- Ratings are stored separately from games, because games are deleted 30 days after their end. Each game is rated exactly once (idempotent by game), also for ends that follow from time alone; the rating is applied when the ended game is next read or compacted. Ratings are deleted with the child's profile or the family. Guardian rows written by an earlier version are dropped on load.

### Review after the game
- When a game has ended, each player sees a short review of **their own** moves on the game page: at most three moments, in game order, of three kinds: a mate in one that was missed, a move after which the opponent could mate at once, and a move after which material was lost (at least about one and a half pawns). Each names a better move. Without findings, the page says that no serious mistake was found.
- Every legal move of the position is scored two half-moves deep with captures (`chess.ScoreMoves`), so the played move can be compared with the best one. This is a simple look for children; the page says that it can be wrong and does not replace a trainer.
- The review is derived from the move list alone, stores nothing and is cached in memory by move list; the first review of a very long game costs up to about a second of server time.

### Not decided here
- **Playtime quota.** The quota policy of issue #19 is not enforced in this slice. A time-based quota does not fit correspondence games that last days; options are a daily number of moves or games, or a quota for live games only. This needs a product decision and its own ADR.
- Games between siblings, with guardians of other families, or in groups.
- A realtime transport. Notifications for invitations and turns are sent as content-free Web Push where that is switched on ([Web Push ADR draft](draft-web-push.md)).
- Other games.

### Consequences
- Positive: chess uses the existing trust model unchanged and adds only a narrower predicate; no new dependency; the rules engine is exhaustively testable; correspondence and live play share one code path and one store.
- Negative / accepted trade-offs: polling creates load and up to about a second of delay in live games; the clock counts network delay against the player who moves (as on most online servers); a server clock jump changes clocks; single process, all games in memory; automatic draws differ from FIDE claim rules.
- Open questions and review gates:
  - Playtime quota semantics (#19) and whether guardians see game activity.
  - Child-safety review: can a game be used for harassment (repeated invitations, resigning at once)? The one-game-per-contact limit and blocking are the current answers.
  - Real-device evidence for the touch target exception, the piece glyphs and screen readers.
  - Realtime transport for the product as a whole.
  - Retention periods (#7).

## Links
- Issues [#19](https://github.com/pblumer/yapuno/issues/19), [#3](https://github.com/pblumer/yapuno/issues/3), [#5](https://github.com/pblumer/yapuno/issues/5), [#7](https://github.com/pblumer/yapuno/issues/7), [#13](https://github.com/pblumer/yapuno/issues/13)
- [Message storage ADR draft](draft-message-storage.md), [contact invitations ADR draft](draft-contact-invitations.md), [report and block ADR draft](draft-report-and-block.md), [Mobile First Experience ADR draft](draft-mobile-first-experience.md)
- [Yapuno Play quota slice (planning)](../planning/yapuno-play-quota.md)

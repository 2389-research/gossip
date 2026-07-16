<!-- ABOUTME: Hard-won lessons for the GOssip project. Review at session start. -->
<!-- ABOUTME: Corrections from Doctor Biz and self-caught process failures live here. -->

# Gotchas

## Architects advise through Palace; the builder owns the workspace (2026-07-16)

Correction from Doctor Biz, recorded from the design room (`msg_01kxmdy0drqf49k9jm7r6sy7ps`):
dedicated architects/reviewers do not perform builder workspace actions — no worktrees, no
implementation plans, no scaffolding, no product-code changes. They coordinate through Palace:
discussion, architectural answers, review when the builder requests it. Agent One briefly
created an unused worktree and was corrected; it was clean and removed.

**Prevention:** workspace ownership is the builder's alone. The builder's direct dispatch from
Doctor Biz is authoritative over assumptions other agents make in the room. Post questions and
checkpoints to the room; surface only material scope conflicts to Doctor Biz. Reviewers answer
after observing, without taking over execution.

## Never write outcome-shaped provenance before the outcome exists (2026-07-16)

While drafting `docs/contract.md`, the Status section was written ahead of the architects'
responses — complete with invented ACK message IDs and four "ratified addenda" that no one had
posted. Caught and rewritten minutes later when the real ACKs arrived carrying different
message IDs and different addenda. Optimistically pre-writing a provenance section is
fabrication with extra steps, and it nearly entered git history as a signed record.

**Prevention:** provenance sections (message IDs, sign-offs, rulings) are filled in only from
observed messages, quoted or paraphrased after reading them. If the document must exist before
the outcome, the section says "pending" — a pending status is true; a predicted message ID is
a lie. This is the project's own epistemics applied to itself: rumor is not observed.

## Palace pagination trap + the crossed workspace claim (2026-07-16)

`palace_ops.py messages --limit N` returns the OLDEST page of the room, not the newest.
Agent Two, reading a small limit as "latest", missed checkpoint 1 entirely and claimed the
gossip workspace (`msg_01kxmej0w0rxjrfs4kr1xchp62`) — Doctor Biz's broadcast location ruling
("build it in here") had landed in multiple channels and, without the room tail visible,
read like a personal reassignment. Two stood down on seeing the full history
(`msg_01kxmephgyskd5mrmfnvmar2d7`); both architects then re-confirmed Agent Three as sole
writer (`msg_01kxmerrq46fbt4mce09yaaf9b`, `msg_01kxmerxqb3594n4pfff8bgsbj`). Same trap bit
this session earlier: `--limit 5` returned the first five messages of the room.

**Prevention:** always fetch with a ceiling above the room size and tail with jq (or use
`events --after <last-observed-id>`); never trust a small `--limit` to mean "newest". Role
assignments don't flip on inference from a broadcast ruling — a reassignment happens
explicitly, on the record, or the standing assignment holds. Cite the last observed message
ID when acting, so crossed posts are detectable.

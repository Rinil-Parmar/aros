# ADR-0001: Record architecture decisions

- **Status:** Accepted
- **Date:** 2026-09-25
- **Deciders:** Rinil Parmar

## Context

Aros has been through two rounds of significant rework. Several of the fixes in
the second round were not new features but the *rediscovery* of constraints that
had already been learned once and then violated again — for example that the
Bubble Tea event loop must never be sent a message from inside `Update`, or that
the vendor CLIs behave completely differently depending on whether tools are
enabled.

That knowledge lived in commit messages and in one person's head. Commit
messages are the wrong home for it: they are ordered by when something changed,
not by what is true now, and nobody reads them before writing new code.

## Decision

We keep Architecture Decision Records in `docs/adr/`, one Markdown file per
decision, numbered sequentially and never renumbered. The format is a trimmed
MADR: Status, Date, Context, Decision, Consequences (explicitly including the
bad ones), Alternatives considered, Notes.

Rules:
- An ADR is immutable once Accepted. To change a decision, write a new ADR and
  set the old one to `Superseded by ADR-NNNN`.
- ADRs describe decisions with lasting architectural consequence, not every
  implementation detail. If getting it wrong again would cost a day, it is an ADR.
- The first ADRs were written retroactively (2026-09-25) for decisions already
  in the code; their **Date** field is the date the decision was actually made,
  taken from git history, not the date it was written down.

## Consequences

**Good**
- A new contributor (or the same person in six months) can read why the code is
  shaped the way it is without bisecting git history.
- Invariants that are expensive to rediscover — ADR-0009, ADR-0013, ADR-0014 —
  now have a canonical statement that a code reviewer can point at.

**Bad**
- ADRs drift from the code if nobody updates them. Superseding is a manual habit,
  not something the compiler enforces.
- Retroactive ADRs reconstruct reasoning after the fact and may be tidier than
  the real decision process was.

**Neutral**
- The v2 design document (`AROS-V2-PLAN.md`) stays separate: it is a *plan*, not
  a record of decisions taken. Decisions extracted from it get their own ADR
  with status Proposed (see ADR-0015).

## Alternatives considered

- **Keep everything in README.md** — the README is user-facing documentation;
  mixing "how to use it" with "why it is built this way" serves neither reader.
- **A single DECISIONS.md** — grows into an unnavigable wall of text and makes
  superseding a decision an edit rather than an append.
- **GitHub issues/discussions** — the repo has none in use, and decisions would
  not travel with a clone of the code.

## Notes

Template in `docs/adr/template.md`; index in `docs/adr/README.md`.

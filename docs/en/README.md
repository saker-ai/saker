# English Documentation

This directory holds the **canonical English documentation** for Saker.

## Convention

- Top-level `docs/*.md` (e.g. `docs/architecture.md`,
  `docs/configuration.md`) remain the historical canonical
  documents — they ship in this state today.
- New documentation written natively in English should land here
  in `docs/en/` going forward, with a parallel translation in
  `docs/zh/`.
- The sync checker (`scripts/check-doc-sync.sh`) compares
  `docs/en/` against `docs/zh/`. When a file exists in both,
  modification times are compared and stale ZH translations are
  warned about.
- If a file exists only at top-level `docs/`, the sync checker
  treats it as canonical EN until it migrates here.

## Migration plan

The 10 top-level `.md` files (`api-reference`, `architecture`,
`configuration`, `deployment`, `development`, `observability`,
`overview`, `security`, `testing`, `third-party-notices`) will move
into `docs/en/` only when their `docs/zh/` counterpart is being
written — the move and the translation land in the same commit so
sync state is never broken.

## Why not move everything now?

A bulk move would invalidate every external link to
`docs/architecture.md` etc. without providing any reader value
(the EN content does not change). The pair-by-pair migration keeps
old URLs working until each translation is ready.

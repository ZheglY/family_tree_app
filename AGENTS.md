# Project working agreement

## Canonical project documents

Before planning or reviewing project work, read:

- `docs/PROJECT_CONCEPT.md` — product vision, scope and invariants.
- `docs/BACKEND_ROADMAP.md` — target backend architecture, domain model, API and implementation order.

If implementation and documentation diverge, point out the discrepancy instead of silently assuming which one is correct.

## Collaboration rule

The project owner wants to implement the entire backend personally for learning and practice.

Unless the owner explicitly asks to write or change backend code, Codex should:

- explain the next implementation step;
- help design entities, contracts and migrations;
- review code written by the owner;
- diagnose errors and suggest focused fixes;
- propose tests and acceptance criteria;
- avoid implementing backend features on the owner's behalf.

Do not change project files when the owner asks only for discussion, explanation, review or planning.

## Product constraints

- Backend: Go modular monolith.
- Primary database: PostgreSQL.
- Binary media storage: private Yandex Object Storage via its S3-compatible API.
- PostgreSQL stores metadata and relations, not image/document bytes.
- An account (`User`) and a person represented in a family tree (`Person`) are different entities.
- A family tree is a graph of persons and typed relationships, not a table with only `mother_id` and `father_id`.
- Every tree-owned record must be authorization-scoped through the tree membership.
- Family data is private by default.
- MVP decisions in `docs/BACKEND_ROADMAP.md` take priority over optional post-MVP ideas.


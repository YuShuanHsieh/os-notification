# Codebase Context Index

Read this index after the root [`AGENTS.md`](../AGENTS.md), then select only the
documents relevant to the current task. Verify all details against the referenced
code before editing because code remains the source of truth.

| Task or question | Read first |
|---|---|
| Understand the system or trace an event end to end | [`architecture.md`](architecture.md) |
| Find the project, class, or test that owns behavior | [`component-map.md`](component-map.md) |
| Change JSON, NATS subjects, priority, batching, limits, acks, URLs, or identity | [`contracts-and-invariants.md`](contracts-and-invariants.md) |
| Change startup, environment variables, NATS setup, or Windows behavior | [`configuration-and-runtime.md`](configuration-and-runtime.md) |
| Add or modify tests; choose validation commands | [`testing.md`](testing.md) |
| Add a feature or make a cross-cutting change | [`change-guide.md`](change-guide.md), plus the relevant files above |

## Other documentation

- [`README.md`](../README.md) is the human-facing setup and usage guide.
- `docs/superpowers/specs/` contains dated design records for completed changes.
- `docs/superpowers/plans/` contains dated implementation plans and rationale.
- `windows_desktop_notification_agent_core_nats_design.html` is the original,
  detailed product design. It is useful for history, but current code and tests
  take precedence where implementation has evolved.

## Maintenance standard

Context files should describe stable, current behavior and point to source files.
Do not paste large code excerpts, plans, session logs, or speculative future work.
Update the relevant context in the same change whenever project boundaries,
contracts, configuration, validation, or important invariants change.

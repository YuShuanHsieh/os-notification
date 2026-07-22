# Agent Guide

This file is the entry point for AI agents working in this repository. Use it to
find the smallest relevant set of project context before reading or changing code.

## Required workflow

1. Read [`context/README.md`](context/README.md).
2. Read the context files listed there for the task. Do not load every file by
   default.
3. Inspect the referenced source and tests; context is a map, while code is the
   source of truth.
4. Before editing, check `git status --short` and preserve unrelated user changes.
5. For behavior changes, add or update tests first when practical.
6. Run the narrowest relevant tests, then the broader suite appropriate to the
   affected projects.
7. Update `context/` when a change makes its guidance inaccurate. Add durable
   facts only; do not record temporary implementation notes there.

## Repository rules

- Keep `NotificationAgent.Core` cross-platform and free of Windows-specific
  dependencies. Put OS integration behind `IToastRenderer` or
  `IIdentityProvider` in a host project.
- Preserve the online-only, at-most-once, best-effort Core NATS delivery model
  unless the task explicitly changes the product contract.
- Keep queues and caches bounded. Do not introduce unbounded buffering.
- Use `TimeProvider` for time-dependent core behavior and `FakeTimeProvider` in
  unit tests; avoid real sleeps and polling in deterministic tests.
- Treat inbound JSON and acknowledgement JSON as external contracts. Change them
  deliberately and update parser/serialization tests and context together.
- Validate externally supplied action and image URLs through `HttpsUrlPolicy`
  before Windows renders or launches them.
- Do not casually change the documented design limits or acknowledgement status
  strings. See [`context/contracts-and-invariants.md`](context/contracts-and-invariants.md).
- The Windows projects are intentionally outside `NotificationAgent.sln`; do not
  add them merely to make solution discovery easier.
- Never edit generated output in `bin/` or `obj/`.

## Validation commands

```bash
# Cross-platform solution: Core, Core tests, ConsoleHost, and TestPublisher
dotnet build NotificationAgent.sln
dotnet test NotificationAgent.sln

# Windows-targeted projects (compile/test separately; runtime verification needs Windows)
dotnet build src/NotificationAgent.Windows/NotificationAgent.Windows.csproj
dotnet test tests/NotificationAgent.Windows.Tests/NotificationAgent.Windows.Tests.csproj

# Focused example
dotnet test tests/NotificationAgent.Core.Tests/NotificationAgent.Core.Tests.csproj \
  --filter "FullyQualifiedName~AggregatorTests"
```

The NATS integration test only exercises the live path when a server is available
at `127.0.0.1:4222`; otherwise it returns without testing that path. Report this
distinction when describing test results.

## Context ownership

Keep this file short and navigational. Put codebase knowledge under `context/`,
categorized by concern. When adding a new subsystem, add it to the context index
and to the relevant component/change map rather than expanding this file into a
second README.

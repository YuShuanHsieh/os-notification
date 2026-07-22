# Testing and Validation

## Test layout

- `tests/NotificationAgent.Core.Tests` covers parsing, deduplication, aggregation,
  bounded pipeline behavior, content shaping, grapheme truncation, URL policy,
  acknowledgement JSON, and an optional live NATS path.
- `tests/NotificationAgent.Windows.Tests` inspects generated toast XML for text,
  attribution, HTTPS actions, and circular avatar images.

Tests use xUnit. Time-dependent core tests use
`Microsoft.Extensions.TimeProvider.Testing.FakeTimeProvider`; new deterministic
timing behavior should follow that pattern.

## Command matrix

| Change scope | Minimum useful validation |
|---|---|
| One Core component | Filter its test class, then run Core.Tests |
| Core public model or cross-cutting pipeline | `dotnet test NotificationAgent.sln` |
| Console startup/composition | `dotnet build NotificationAgent.sln`; smoke with NATS when relevant |
| Windows content or URL behavior | Windows.Tests plus Windows project build |
| Windows identity/startup/native renderer | Windows build/tests, then report that real Windows verification is still required |
| Package/project/target framework | Build the solution and both standalone Windows projects |
| NATS subjects, acks, or subscription | Core tests plus live NATS integration/smoke test when available |

Commands:

```bash
dotnet build NotificationAgent.sln
dotnet test NotificationAgent.sln

dotnet test tests/NotificationAgent.Core.Tests/NotificationAgent.Core.Tests.csproj \
  --filter "FullyQualifiedName~EventPipelineTests"

dotnet build src/NotificationAgent.Windows/NotificationAgent.Windows.csproj
dotnet test tests/NotificationAgent.Windows.Tests/NotificationAgent.Windows.Tests.csproj
```

## Integration-test interpretation

`NatsIntegrationTests` probes `127.0.0.1:4222`. When NATS is unavailable, its test
method writes a skipped-style message and returns; xUnit may still report the test
as passed. Therefore a green suite alone does not prove the live NATS path ran.
State whether NATS was available when reporting validation.

Core NATS has no replay, so integration setup intentionally allows subscriptions
to settle before publishing. Avoid making those tests dependent on delivery before
a subscription exists.

## Test design conventions

- Test observable contracts and boundary cases, not private implementation shape.
- For a bug, add a regression test that fails for the original cause.
- Prefer fakes for `IToastRenderer`, `ITelemetryPublisher`, and
  `IIdentityProvider` in core tests.
- Use `FakeTimeProvider.Advance` for batching and TTL; do not add wall-clock sleeps
  for deterministic unit behavior.
- When changing wire JSON, assert exact property naming, defaults, omitted nulls,
  and invalid-input behavior.
- Windows toast tests should inspect generated XML and include both accepted and
  rejected URL cases.

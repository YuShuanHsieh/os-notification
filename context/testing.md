# Testing and Validation

## Test layout

- `tests/NotificationAgent.Core.Tests` covers parsing, deduplication, aggregation,
  bounded pipeline behavior, content shaping, grapheme truncation, URL policy,
  acknowledgement JSON, and an optional live NATS path.
- `tests/NotificationAgent.Windows.Tests` inspects generated toast XML for text,
  attribution, HTTPS actions, and circular avatar images.
- Rust unit tests live beside the implementation under `rust/notify-agent-core`
  and include a live-path NATS integration test in
  `rust/notify-agent-core/tests/nats_integration.rs`.
- Go tests live beside the implementation as `golang/internal/*/*_test.go`
  (table-driven, standard `testing` package) plus a few `golang/cmd/*/*_test.go`
  files for CLI parsing/rendering helpers. `golang/internal/host/host_test.go`
  includes a live-path NATS integration test that skips cleanly (no failure)
  when no server is reachable at `127.0.0.1:4222`, mirroring the C#/Rust
  integration-test pattern.

.NET tests use xUnit; Go tests use the standard library `testing` package (see
above). Time-dependent .NET core tests use
`Microsoft.Extensions.TimeProvider.Testing.FakeTimeProvider`; new deterministic
timing behavior should follow that pattern.

Every project inherits repository-wide lint settings from `Directory.Build.props`
and `.editorconfig`. The .NET SDK runs its recommended analyzer set, StyleCop supplies
established C# source-style rules, and warnings fail builds. Copyright headers and
mandatory public XML documentation are intentionally excluded; related compact
types may share a source file.

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
dotnet format NotificationAgent.sln --verify-no-changes --no-restore

dotnet test tests/NotificationAgent.Core.Tests/NotificationAgent.Core.Tests.csproj \
  --filter "FullyQualifiedName~EventPipelineTests"

dotnet build src/NotificationAgent.Windows/NotificationAgent.Windows.csproj
dotnet test tests/NotificationAgent.Windows.Tests/NotificationAgent.Windows.Tests.csproj
dotnet format tests/NotificationAgent.Windows.Tests/NotificationAgent.Windows.Tests.csproj \
  --verify-no-changes --no-restore

cd rust
cargo test
cargo build
```

For the Rust Windows head, use the repository's Docker cross-build workflow:

```bash
cd rust
./scripts/build-windows-docker.sh
```

```bash
cd golang
gofmt -l .
go vet ./...
go build ./...
go test ./...
go test -race ./...
```

For the Go Windows head, cross-compile with the standard Go toolchain (no
mingw or other cross-toolchain needed):

```bash
cd golang
GOOS=windows GOARCH=amd64 go build -o notify-agent-windows.exe ./cmd/notify-agent-windows
```

Run formatting verification after restore/build because `--no-restore` keeps the
lint check deterministic. To fix supported formatting diagnostics locally, remove
`--verify-no-changes`, inspect the resulting diff, and then rerun verification.

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
- Rust tray UI (`rust/notify-agent-windows/src/tray.rs`) is not covered by
  automated tests; verify it manually on Windows by checking immediate icon
  appearance, version/Close menu behavior, clean exit, and the startup-failure
  tooltip path. Rust Windows compilation does not prove desktop behavior.
- The Go Windows head's PowerShell-invoked toast submission
  (`golang/cmd/notify-agent-windows/renderer.go`) and its
  `github.com/getlantern/systray`-based tray icon (`tray.go`) both have no
  automated coverage and require a real Windows desktop to verify manually
  (icon appearance, version/Close menu, clean exit, startup-failure tooltip,
  and an actual rendered toast including the avatar-image path). Successful
  `GOOS=windows GOARCH=amd64 go build` cross-compilation proves the code
  compiles, not that it behaves correctly at runtime.

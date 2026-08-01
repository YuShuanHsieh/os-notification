# Offline .NET build image

`docker/dotnet-offline.Dockerfile` builds an image that can run this repo's
C# build/test/format commands with no network access — for air-gapped or
network-restricted CI environments. Two steps, deliberately separate:

## 1. Refresh (needs network)

Rebuild the image whenever a `PackageReference` changes (version bump,
add, or remove) in any of the six projects listed below, or periodically to
pick up a newer .NET 10 SDK patch:

```bash
docker build -f docker/dotnet-offline.Dockerfile -t notification-agent-offline .
```

This is the *only* step that talks to `nuget.org`. Before any restore runs, it
checks that all six `packages.lock.json` files listed below actually exist —
failing fast with a clear message if one was deleted (NuGet's own
`--locked-mode` restore silently regenerates a missing lock file instead of
failing, so this guard exists to catch that case explicitly). It then
restores every project with `--locked-mode`, which fails the build if a
project's `packages.lock.json` is *stale* (its content hash doesn't match the
`PackageReference`s it was generated from). If you changed dependencies,
regenerate the affected lock file(s) first:

```bash
docker run --rm -u "$(id -u):$(id -g)" -e HOME=/tmp \
  -v "$(pwd)":/repo -w /repo mcr.microsoft.com/dotnet/sdk:10.0 \
  dotnet restore <path-to-csproj>
```

then commit the updated `packages.lock.json` alongside the dependency change,
and rebuild the image.

Projects with a lock file: `src/NotificationAgent.Core`,
`src/NotificationAgent.ConsoleHost`, `src/NotificationAgent.Windows`,
`tests/NotificationAgent.Core.Tests`, `tests/NotificationAgent.Windows.Tests`,
`tools/TestPublisher`.

## 2. Offline build/test/format (network disabled)

Once the image is built, verify the network-disabled path:

```bash
docker run --rm --network none notification-agent-offline \
  dotnet build NotificationAgent.sln --no-restore
docker run --rm --network none notification-agent-offline \
  dotnet test NotificationAgent.sln --no-restore
docker run --rm --network none notification-agent-offline \
  dotnet format NotificationAgent.sln --verify-no-changes --no-restore
docker run --rm --network none notification-agent-offline \
  dotnet build src/NotificationAgent.Windows/NotificationAgent.Windows.csproj --no-restore
docker run --rm --network none notification-agent-offline \
  dotnet build tests/NotificationAgent.Windows.Tests/NotificationAgent.Windows.Tests.csproj --no-restore
```

`--no-restore` guarantees these never attempt a NuGet restore; `--network none`
is what actually proves it — anything that needed the network would fail
loudly here rather than silently succeeding.

`NotificationAgent.Windows` and `NotificationAgent.Windows.Tests` are
excluded from `NotificationAgent.sln` (see `AGENTS.md`) and built as separate
commands. Running `NotificationAgent.Windows.Tests`' actual tests still
requires a real Windows machine, per `AGENTS.md` — this image only proves
they *compile* offline.

## Out of scope

- **Rust / `rust-agent`:** the Rust workspace (pinned `rust-toolchain.toml`,
  vendored crates, `x86_64-pc-windows-gnu` cross-compile) only exists on the
  unmerged `rust-agent` branch (PR #4). A sibling `docker/rust-offline.Dockerfile`
  following the same refresh/offline split is deferred until that branch
  merges — see [issue #11](https://github.com/YuShuanHsieh/os-notification/issues/11).
- Publishing this image to GHCR or any registry.
- A GitHub Actions (or other CI) workflow using this image automatically.

# .NET Offline Build Image Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a Docker image that can build and test the C# side of this repo (`NotificationAgent.sln` plus the Windows-only projects) with no network access at `dotnet build`/`test`/`format` time.

**Architecture:** A single-stage `docker/dotnet-offline.Dockerfile` (`FROM mcr.microsoft.com/dotnet/sdk:10.0`) copies the whole repo in and runs `dotnet restore --locked-mode` during `docker build` (network available then). Every project gets a committed `packages.lock.json` (via a new `RestorePackagesWithLockFile` property in `Directory.Build.props`) so that restore is reproducible and verifiable. Consumers then run `docker run --network none <image> dotnet build/test/format ... --no-restore` — nothing in that path can touch the network, because everything was already resolved into the image at build time.

**Tech Stack:** .NET 10 SDK (`mcr.microsoft.com/dotnet/sdk:10.0`, confirmed to resolve to SDK `10.0.302`), Docker, NuGet lock files (`packages.lock.json`).

## Global Constraints

- Base image: `mcr.microsoft.com/dotnet/sdk:10.0`.
- `RestorePackagesWithLockFile=true` is added once, repo-wide, in `Directory.Build.props` (matches how analyzer settings are already centralized there).
- Six projects need a committed `packages.lock.json`: `src/NotificationAgent.Core`, `src/NotificationAgent.ConsoleHost`, `src/NotificationAgent.Windows`, `tests/NotificationAgent.Core.Tests`, `tests/NotificationAgent.Windows.Tests`, `tools/TestPublisher`.
- `src/NotificationAgent.Windows` and `tests/NotificationAgent.Windows.Tests` are intentionally excluded from `NotificationAgent.sln` (see `AGENTS.md`) — restore/build them as separate commands, never by adding them to the `.sln`.
- Offline verification always runs as `docker run --rm --network none notification-agent-offline <command> --no-restore`. The refresh step (`docker build`) is the only step allowed to need network.
- Out of scope (see design doc's Non-goals): the Rust/`rust-agent` image, GHCR publishing, a GitHub Actions/CI workflow, running `NotificationAgent.Windows.Tests`' actual tests (compile-only, per `AGENTS.md`), and any persistent/bind-mounted dev container usage.
- Design doc: `docs/superpowers/specs/2026-07-23-dotnet-offline-build-image-design.md`. Source issue: [#11](https://github.com/YuShuanHsieh/os-notification/issues/11).

---

### Task 1: Reproducible restores via `packages.lock.json`

**Files:**
- Modify: `Directory.Build.props`
- Create: `src/NotificationAgent.Core/packages.lock.json`
- Create: `src/NotificationAgent.ConsoleHost/packages.lock.json`
- Create: `src/NotificationAgent.Windows/packages.lock.json`
- Create: `tests/NotificationAgent.Core.Tests/packages.lock.json`
- Create: `tests/NotificationAgent.Windows.Tests/packages.lock.json`
- Create: `tools/TestPublisher/packages.lock.json`

**Interfaces:**
- Produces: the `RestorePackagesWithLockFile=true` property (inherited by every project via `Directory.Build.props`) and six committed lock files. Task 2's Dockerfile depends on these lock files existing and being accurate — its `dotnet restore --locked-mode` steps fail if they're missing or stale.

- [ ] **Step 1: Add the lock-file property to `Directory.Build.props`**

Current content is:

```xml
<Project>
  <PropertyGroup>
    <!-- Apply the SDK's recommended analyzer set to every project. -->
    <AnalysisLevel>latest</AnalysisLevel>
    <AnalysisMode>Recommended</AnalysisMode>
    <EnforceCodeStyleInBuild>true</EnforceCodeStyleInBuild>
    <TreatWarningsAsErrors>true</TreatWarningsAsErrors>
    <CodeAnalysisTreatWarningsAsErrors>true</CodeAnalysisTreatWarningsAsErrors>
  </PropertyGroup>
  ...
```

Add `RestorePackagesWithLockFile` to that same `PropertyGroup`:

```xml
<Project>
  <PropertyGroup>
    <!-- Apply the SDK's recommended analyzer set to every project. -->
    <AnalysisLevel>latest</AnalysisLevel>
    <AnalysisMode>Recommended</AnalysisMode>
    <EnforceCodeStyleInBuild>true</EnforceCodeStyleInBuild>
    <TreatWarningsAsErrors>true</TreatWarningsAsErrors>
    <CodeAnalysisTreatWarningsAsErrors>true</CodeAnalysisTreatWarningsAsErrors>
    <!-- Pins exact package versions per project so restores are reproducible
         offline (docker/dotnet-offline.Dockerfile) and verifiable in CI. -->
    <RestorePackagesWithLockFile>true</RestorePackagesWithLockFile>
  </PropertyGroup>
  ...
```

Leave the rest of the file (the `StyleCop.Analyzers` `ItemGroup`) unchanged.

- [ ] **Step 2: Generate the six lock files using the containerized SDK**

The local machine has no `dotnet` CLI installed, so generate lock files using the same SDK image the offline build will use. Run this from the repo root (each `docker run` needs network — this is the "refresh" side of the design, done once now):

```bash
for proj in \
  src/NotificationAgent.Core/NotificationAgent.Core.csproj \
  src/NotificationAgent.ConsoleHost/NotificationAgent.ConsoleHost.csproj \
  src/NotificationAgent.Windows/NotificationAgent.Windows.csproj \
  tests/NotificationAgent.Core.Tests/NotificationAgent.Core.Tests.csproj \
  tests/NotificationAgent.Windows.Tests/NotificationAgent.Windows.Tests.csproj \
  tools/TestPublisher/TestPublisher.csproj; do
  echo "=== restoring $proj ==="
  docker run --rm -u "$(id -u):$(id -g)" -e HOME=/tmp \
    -v "$(pwd)":/repo -w /repo mcr.microsoft.com/dotnet/sdk:10.0 \
    dotnet restore "$proj"
done
```

`-u "$(id -u):$(id -g)" -e HOME=/tmp` makes the container run as the host user with a writable `$HOME`, so the generated `packages.lock.json` files land owned by the host user instead of `root` (the container's default user needs a writable home directory for its first-run telemetry sentinel; without this flag pair the restore fails with a permission error writing to `/root`).

Expected output per project: `Restored /repo/<path-to-csproj> (in N sec).` with no errors. A harmless one-line `An issue was encountered verifying workloads...` warning may appear first — ignore it, restore still succeeds.

- [ ] **Step 3: Verify all six lock files were created**

```bash
ls src/NotificationAgent.Core/packages.lock.json \
   src/NotificationAgent.ConsoleHost/packages.lock.json \
   src/NotificationAgent.Windows/packages.lock.json \
   tests/NotificationAgent.Core.Tests/packages.lock.json \
   tests/NotificationAgent.Windows.Tests/packages.lock.json \
   tools/TestPublisher/packages.lock.json
```

Expected: all six paths listed, no "No such file" errors.

- [ ] **Step 4: Verify `--locked-mode` restore succeeds (proves the property + lock files work together)**

```bash
docker run --rm -u "$(id -u):$(id -g)" -e HOME=/tmp \
  -v "$(pwd)":/repo -w /repo mcr.microsoft.com/dotnet/sdk:10.0 \
  dotnet restore NotificationAgent.sln --locked-mode

docker run --rm -u "$(id -u):$(id -g)" -e HOME=/tmp \
  -v "$(pwd)":/repo -w /repo mcr.microsoft.com/dotnet/sdk:10.0 \
  dotnet restore src/NotificationAgent.Windows/NotificationAgent.Windows.csproj --locked-mode

docker run --rm -u "$(id -u):$(id -g)" -e HOME=/tmp \
  -v "$(pwd)":/repo -w /repo mcr.microsoft.com/dotnet/sdk:10.0 \
  dotnet restore tests/NotificationAgent.Windows.Tests/NotificationAgent.Windows.Tests.csproj --locked-mode
```

Expected: all three commands print `Restored ...` lines for every project involved, exit code 0, no `NU1403` errors.

- [ ] **Step 5: Commit**

```bash
git add Directory.Build.props \
  src/NotificationAgent.Core/packages.lock.json \
  src/NotificationAgent.ConsoleHost/packages.lock.json \
  src/NotificationAgent.Windows/packages.lock.json \
  tests/NotificationAgent.Core.Tests/packages.lock.json \
  tests/NotificationAgent.Windows.Tests/packages.lock.json \
  tools/TestPublisher/packages.lock.json
git commit -m "build: pin NuGet restores with packages.lock.json"
```

---

### Task 2: Offline build image

**Files:**
- Create: `.dockerignore`
- Create: `docker/dotnet-offline.Dockerfile`

**Interfaces:**
- Consumes: the `RestorePackagesWithLockFile` property and six `packages.lock.json` files from Task 1 (the Dockerfile's `--locked-mode` restores fail without them).
- Produces: a local image tagged `notification-agent-offline` that Task 3 builds on (for the negative-control check) and that `docker/README.md` (Task 3) documents how to use.

- [ ] **Step 1: Write `.dockerignore`**

```
.git
.worktrees
**/bin/
**/obj/
```

- [ ] **Step 2: Write `docker/dotnet-offline.Dockerfile`**

```dockerfile
# Offline-capable .NET build image. See docker/README.md for the refresh
# (needs network) vs. offline build/test (no network) workflow.
FROM mcr.microsoft.com/dotnet/sdk:10.0

WORKDIR /repo
COPY . .

# All three restores need network here (docker build time) to reach nuget.org.
# --locked-mode fails the build if packages.lock.json is missing or doesn't
# match the referenced packages, so a stale lock file surfaces immediately.
RUN dotnet restore NotificationAgent.sln --locked-mode
RUN dotnet restore src/NotificationAgent.Windows/NotificationAgent.Windows.csproj --locked-mode
RUN dotnet restore tests/NotificationAgent.Windows.Tests/NotificationAgent.Windows.Tests.csproj --locked-mode
```

> **Amendment (discovered during Task 3, human-approved fix in commit `757fe4c`):**
> `dotnet restore --locked-mode` on SDK `10.0.302` was empirically verified (4 independent
> reproductions, including an isolated clean clone) to only fail on a *stale* lock file
> (`NU1403`) — a fully *missing* `packages.lock.json` is silently regenerated (exit 0). The
> comment above claiming `--locked-mode` catches "missing or doesn't match" is therefore
> **wrong** and was corrected. A guard `RUN` step was added right after `COPY . .`, before
> any restore, checking all six lock files exist and failing fast with a clear message if
> one is absent:
>
> ```dockerfile
> RUN for f in \
>         src/NotificationAgent.Core/packages.lock.json \
>         src/NotificationAgent.ConsoleHost/packages.lock.json \
>         src/NotificationAgent.Windows/packages.lock.json \
>         tests/NotificationAgent.Core.Tests/packages.lock.json \
>         tests/NotificationAgent.Windows.Tests/packages.lock.json \
>         tools/TestPublisher/packages.lock.json; \
>     do \
>         test -f "$f" || { echo "missing lock file: $f" >&2; exit 1; }; \
>     done
> ```
>
> See the design doc's Error handling section for the full explanation. Task 3's negative
> control (below) tests this guard, not raw `--locked-mode`, for the missing-file case.

- [ ] **Step 3: Build the image**

```bash
docker build -f docker/dotnet-offline.Dockerfile -t notification-agent-offline .
```

Expected: build completes successfully (ends with the final layer committed and the image tagged; recent Docker/BuildKit output ends with `naming to docker.io/library/notification-agent-offline` or similar, no error exit code). This step needs network — it's the "refresh" half of the design.

- [ ] **Step 4: Verify offline build/test/format succeed with network disabled**

Run each of the following. All must succeed with `--network none`:

```bash
docker run --rm --network none notification-agent-offline \
  dotnet build NotificationAgent.sln --no-restore
```
Expected: ends with `Build succeeded.`

```bash
docker run --rm --network none notification-agent-offline \
  dotnet test NotificationAgent.sln --no-restore
```
Expected: ends with a `Passed!` summary line for each test project (no failures, no errors reaching the network).

```bash
docker run --rm --network none notification-agent-offline \
  dotnet format NotificationAgent.sln --verify-no-changes --no-restore
```
Expected: exits 0 with no formatting violations reported.

```bash
docker run --rm --network none notification-agent-offline \
  dotnet build src/NotificationAgent.Windows/NotificationAgent.Windows.csproj --no-restore
```
Expected: ends with `Build succeeded.`

```bash
docker run --rm --network none notification-agent-offline \
  dotnet build tests/NotificationAgent.Windows.Tests/NotificationAgent.Windows.Tests.csproj --no-restore
```
Expected: ends with `Build succeeded.` (compile-only — running this project's tests needs a real Windows machine, per `AGENTS.md`, unrelated to network access).

- [ ] **Step 5: Commit**

```bash
git add .dockerignore docker/dotnet-offline.Dockerfile
git commit -m "build: add offline .NET build image (docker/dotnet-offline.Dockerfile)"
```

---

### Task 3: Prove lock-file enforcement and document the workflow

**Files:**
- Create: `docker/README.md`

**Interfaces:**
- Consumes: the `notification-agent-offline` image workflow from Tasks 1–2, including the file-existence guard step added in the Task 2 fix (commit `757fe4c`) — documents and re-verifies it; doesn't change the Dockerfile or lock files further.

- [ ] **Step 1: Prove a missing lock file is actually caught (negative control)**

> **Amendment:** the original version of this step expected `--locked-mode` itself to fail
> on a missing lock file with `NU1004`. That was verified false (see Task 2's amendment
> above) — `--locked-mode` silently regenerates a missing lock file instead. The guard step
> added in commit `757fe4c` is what actually catches this now; the command and file removed
> are unchanged, only the expected output differs.

```bash
mv src/NotificationAgent.Core/packages.lock.json /tmp/packages.lock.json.bak
docker build -f docker/dotnet-offline.Dockerfile -t notification-agent-offline-negative-test . 2>&1 | tail -20
```

Expected: the build **fails** at the guard `RUN` step (before any `dotnet restore` step runs), with:

```
missing lock file: src/NotificationAgent.Core/packages.lock.json
```

and a non-zero exit code. This confirms a deleted lock file is genuinely caught, not silently regenerated.

- [ ] **Step 2: Restore the lock file and confirm the repo is clean**

```bash
mv /tmp/packages.lock.json.bak src/NotificationAgent.Core/packages.lock.json
git status --short
```

Expected: no output from `git status --short` (working tree matches the Task 1 commit).

- [ ] **Step 3: Write `docker/README.md`**

```markdown
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
```

- [ ] **Step 4: Commit**

```bash
git add docker/README.md
git commit -m "docs: document the offline .NET build image workflow"
```

# .NET 10 Upgrade Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Retarget every active project and its current documentation from .NET 8 to .NET 10 without changing application behavior.

**Architecture:** Keep target frameworks explicit in each project file, matching the repository's existing structure. Align the .NET-specific testing package with .NET 10, leave all other dependencies and the historical implementation plan unchanged, and verify the solution plus the separately built Windows head with a .NET 10 SDK.

**Tech Stack:** .NET 10 SDK, MSBuild, NuGet, xUnit, and the Windows App SDK notification backend that was present before the subsequent notification migration

---

## Task 1: Retarget Active Projects

**Files:**
- Modify: `src/NotificationAgent.Core/NotificationAgent.Core.csproj`
- Modify: `src/NotificationAgent.ConsoleHost/NotificationAgent.ConsoleHost.csproj`
- Modify: `src/NotificationAgent.Windows/NotificationAgent.Windows.csproj`
- Modify: `tools/TestPublisher/TestPublisher.csproj`
- Modify: `tests/NotificationAgent.Core.Tests/NotificationAgent.Core.Tests.csproj`

- [x] **Step 1: Change the cross-platform target frameworks**

In Core, ConsoleHost, TestPublisher, and Core.Tests, replace:

```xml
<TargetFramework>net8.0</TargetFramework>
```

with:

```xml
<TargetFramework>net10.0</TargetFramework>
```

- [x] **Step 2: Change the Windows target framework**

In `src/NotificationAgent.Windows/NotificationAgent.Windows.csproj`, replace:

```xml
<TargetFramework>net8.0-windows10.0.19041.0</TargetFramework>
```

with:

```xml
<TargetFramework>net10.0-windows10.0.19041.0</TargetFramework>
```

- [x] **Step 3: Align the .NET-specific testing dependency**

In `tests/NotificationAgent.Core.Tests/NotificationAgent.Core.Tests.csproj`, replace:

```xml
<PackageReference Include="Microsoft.Extensions.TimeProvider.Testing" Version="8.*" />
```

with:

```xml
<PackageReference Include="Microsoft.Extensions.TimeProvider.Testing" Version="10.*" />
```

- [x] **Step 4: Confirm the old SDK rejects the new target**

Using a shell where `dotnet` resolves to the old .NET 8 SDK, run:

```bash
dotnet build NotificationAgent.sln --no-restore
```

Expected: FAIL with `NETSDK1045`, confirming the projects now require a .NET 10 SDK rather than silently continuing to build with .NET 8.

## Task 2: Update Current Documentation

**Files:**
- Modify: `README.md`

- [x] **Step 1: Update the architecture and project target references**

Replace current prose occurrences of `.NET 8` with `.NET 10`, all four README target occurrences of `net8.0` with `net10.0`, and the Windows target `net8.0-windows10.0.19041.0` with `net10.0-windows10.0.19041.0`.

- [x] **Step 2: Update the SDK installation instructions**

Change the prerequisite heading to `.NET 10 SDK` and replace:

```bash
bash /tmp/dotnet-install.sh --channel 8.0
```

with:

```bash
bash /tmp/dotnet-install.sh --channel 10.0
```

- [x] **Step 3: Confirm active documentation no longer advertises .NET 8**

Run:

```bash
rg -n '(\.NET 8|net8\.0|channel 8\.0)' README.md src tests tools
```

Expected: no matches. Do not alter `docs/superpowers/plans/2026-07-15-windows-desktop-notification-agent.md`, whose .NET 8 references are historical.

## Task 3: Verify with .NET 10

**Files:**
- Verify: `NotificationAgent.sln`
- Verify: `src/NotificationAgent.Windows/NotificationAgent.Windows.csproj`

- [x] **Step 1: Make a .NET 10 SDK available**

Install the .NET 10 SDK into a temporary directory so verification does not alter repository configuration or replace the user's existing .NET 8 installation:

```bash
bash /tmp/dotnet-install.sh --channel 10.0 --install-dir /tmp/dotnet10
```

Expected: installation succeeds and `/tmp/dotnet10/dotnet --version` reports `10.0.x`.

- [x] **Step 2: Restore and build the Linux solution**

Run:

```bash
/tmp/dotnet10/dotnet build NotificationAgent.sln
```

Expected: exit code 0 with 0 build errors.

- [x] **Step 3: Run the complete test suite**

Run:

```bash
/tmp/dotnet10/dotnet test NotificationAgent.sln --no-build
```

Expected: exit code 0 with all runnable tests passing; the NATS integration test may skip when no server is available.

- [x] **Step 4: Build the Windows head separately**

Run:

```bash
/tmp/dotnet10/dotnet build src/NotificationAgent.Windows/NotificationAgent.Windows.csproj
```

Expected: exit code 0 with 0 build errors. This project is intentionally not part of `NotificationAgent.sln`.

- [x] **Step 5: Check the final diff and active version references**

Run:

```bash
git diff --check
rg -n '(net10\.0|Version="10\.\*"|\.NET 10|channel 10\.0)' README.md src tests tools
git diff -- README.md src tests tools
```

Expected: no whitespace errors; all active targets and current documentation point to .NET 10; only the scoped project metadata, testing dependency, and README have changed.

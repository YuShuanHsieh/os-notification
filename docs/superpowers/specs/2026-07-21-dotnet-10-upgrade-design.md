# .NET 10 Upgrade Design

## Goal

Upgrade the active notification-agent projects from .NET 8 to .NET 10 while preserving application behavior and the existing repository structure.

## Scope

- Retarget the four cross-platform projects and the test project from `net8.0` to `net10.0`.
- Retarget the Windows project from `net8.0-windows10.0.19041.0` to `net10.0-windows10.0.19041.0`.
- Update `Microsoft.Extensions.TimeProvider.Testing` from `8.*` to `10.*` so the .NET-specific test dependency aligns with the target framework.
- Update current README references, prerequisites, and installation commands from .NET 8 to .NET 10.

The dated implementation plan at `docs/superpowers/plans/2026-07-15-windows-desktop-notification-agent.md` remains unchanged as a historical record. Other NuGet package versions, source behavior, solution membership, and Windows platform requirements remain unchanged.

## Approach

Edit each project file directly. This follows the repository's current explicit-target pattern and avoids introducing a `Directory.Build.props` file or `global.json` solely for this upgrade. Multi-targeting is unnecessary because the requested outcome is a complete move to .NET 10 rather than a compatibility transition.

## Compatibility and Error Handling

The upgrade changes build metadata only. Any incompatibility will surface during NuGet restore or compilation. Such failures will be addressed only when they are caused by the .NET 10 retargeting; unrelated dependency upgrades or refactors are outside scope.

## Verification

Using a .NET 10 SDK:

1. Restore and build `NotificationAgent.sln`.
2. Run the full test suite in `NotificationAgent.sln`.
3. Build `src/NotificationAgent.Windows/NotificationAgent.Windows.csproj` separately because it is intentionally excluded from the solution.
4. Search active project files and the README to confirm no current .NET 8 references remain. Historical references in the dated implementation plan are expected and permitted.

No new unit test is needed because this is a configuration-only change; restore, compilation, and the existing test suite directly verify the upgrade.

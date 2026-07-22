# Remove Windows App SDK Design

## Goal

Replace the Windows App SDK notification integration with Windows Community Toolkit notifications so the Windows head can be compiled with the standalone .NET 10 SDK on Linux or WSL and deployed without the Windows App SDK runtime.

## Scope

- Replace `Microsoft.WindowsAppSDK` with `Microsoft.Toolkit.Uwp.Notifications` 7.1.3.
- Override the toolkit's vulnerable `System.Drawing.Common` 4.7.0 transitive dependency with the current .NET 10 servicing package.
- Remove Windows App SDK-specific build properties and the now-unneeded Windows SDK Build Tools package.
- Replace `AppNotificationBuilder`, `AppNotificationButton`, and `AppNotificationManager` usage with the toolkit's toast builder and compatibility manager.
- Preserve the current toast title, message, attribution, HTTPS action button, submission timestamp, and acknowledgement behavior.
- Add focused tests for generated notification content.
- Update current README architecture, setup, and Windows-build documentation.

The NATS pipeline, identity providers, single-instance mutex, target Windows version, runtime identifiers, and all Core behavior remain unchanged.

## Dependencies and Build Model

`src/NotificationAgent.Windows` remains an unpackaged `net10.0-windows10.0.19041.0` executable with `EnableWindowsTargeting` enabled. It removes `Microsoft.WindowsAppSDK`, `Microsoft.Windows.SDK.BuildTools`, `WindowsPackageType`, and `WindowsAppSDKSelfContained`. The replacement `Microsoft.Toolkit.Uwp.Notifications` package supplies the toast-content builder and compatibility layer without Windows App SDK PRI/MSIX build tasks or runtime deployment. A direct `System.Drawing.Common` 10.0.10 reference replaces the toolkit's vulnerable 4.7.0 transitive version without changing application behavior.

Version 7.1.3 is an explicit legacy compatibility choice: the toolkit notification component is archived, but this package preserves unpackaged desktop notification support without adding the Windows App SDK runtime. The lifecycle risk is accepted for this migration and must be revisited when a maintained alternative supports the same standalone deployment model.

The Windows executable must compile with `dotnet build` from Linux or WSL. Running the executable and displaying a notification remain Windows-only operations.

## Components and Data Flow

`WindowsToastRenderer` continues to implement `IToastRenderer` and consume an unchanged `ToastRequest`:

1. Create a `ToastContentBuilder`.
2. Add the required title and message.
3. Add attribution text when present.
4. Validate the optional action with `ActionUrlPolicy`.
5. For a valid HTTPS action, add a `ToastButton` configured with protocol activation so Windows opens the URI without spawning a shell.
6. Submit the notification through the toolkit compatibility notifier.
7. Return `DateTimeOffset.UtcNow` only after submission succeeds.

Notification-content construction will be isolated from submission in a focused internal factory. This makes the XML payload testable without displaying a Windows notification.

`Program.cs` no longer registers or unregisters `AppNotificationManager`. The toolkit compatibility layer performs the per-user registration needed by an unpackaged process. `ToastNotificationManagerCompat.Uninstall()` is not called during normal shutdown because it is reserved for actual application removal.

## Error Handling and Security

The existing `ActionUrlPolicy` remains the only gate for action URIs. Missing, malformed, non-HTTPS, credential-bearing, or oversized URLs produce a notification without an action button.

Toolkit submission exceptions propagate from `ShowAsync`, matching current behavior. Because the pipeline publishes `submitted_to_windows` only after `ShowAsync` returns, a rejected submission does not produce a false success acknowledgement.

No shell command, PowerShell process, or installer-time COM registration is introduced.

## Verification

- Add tests that inspect generated toast content for title and message, optional attribution, a valid HTTPS protocol button, and omission of unsafe actions.
- Run all existing Core tests.
- Build `NotificationAgent.sln` with the .NET 10 SDK.
- Build `src/NotificationAgent.Windows/NotificationAgent.Windows.csproj` with the standalone .NET 10 SDK on Linux, confirming Visual Studio PRI/MSIX tasks are no longer required.
- On Windows, manually start the agent, publish a test event, confirm the notification appears, and confirm its action button opens the expected HTTPS URL.

The Linux build and automated tests are required before completion. The Windows visual smoke test is documented as a remaining manual verification when a Windows host is unavailable.

## Documentation

README references to Windows App SDK notifications and the Visual Studio-specific build path will be replaced with Community Toolkit and standalone .NET SDK instructions. The dated historical implementation plan remains unchanged.

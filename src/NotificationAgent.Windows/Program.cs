using Microsoft.Extensions.Logging;
using NotificationAgent.Core.Hosting;
using NotificationAgent.Core.Identity;
using NotificationAgent.Windows;

// Top-level statements can't carry [STAThread]; set it explicitly as the very first
// statement, before anything else touches this thread. WinForms' Application.Run
// requires STA (design: system tray icon).
Thread.CurrentThread.SetApartmentState(ApartmentState.STA);

// One instance per interactive session: "Local\" mutexes are session-scoped,
// so two signed-in users each get their own agent (design §2, ADR-001).
using var singleInstance = new Mutex(
    initiallyOwned: true,
    @"Local\DesktopNotificationAgent", out var isFirstInstance);
if (!isFirstInstance)
{
    return;
}

// Feature: app settings file. %LOCALAPPDATA%\DesktopNotificationAgent\settings.json is
// optional; env vars still win over it, and built-in defaults apply when neither is set
// (WindowsSettings.Resolve owns the per-field precedence).
var settingsPath = WindowsSettings.DefaultPath;
var settingsFileExists = File.Exists(settingsPath);
var settingsDiagnostics = new List<SettingsDiagnostic>();
var settingsFile = WindowsSettings.LoadFile(settingsPath, settingsDiagnostics);
var settings = WindowsSettings.Resolve(settingsFile, Environment.GetEnvironmentVariable, settingsDiagnostics);

using var loggerFactory = LoggerFactory.Create(builder => builder
    .AddSimpleConsole(o => o.SingleLine = true)
    .SetMinimumLevel(settings.LogLevel));
var startupLogger = loggerFactory.CreateLogger("Startup");

// Replay diagnostics gathered while loading/resolving settings: the logger's minimum level
// comes from the settings file itself, so it couldn't have been passed down to log these as
// they happened (see SettingsDiagnostic).
foreach (var diagnostic in settingsDiagnostics)
{
    diagnostic.Replay(startupLogger);
}

startupLogger.StartupSettingsFile(settingsPath, settingsFileExists);

var options = settings.Options;
var clientId = settings.AadClientId;
MsalIdentityProvider? msalIdentity =
    clientId is { Length: > 0 }
        ? new MsalIdentityProvider(
            clientId,
            settings.AadTenantId,
            settings.DeviceId,
            loggerFactory.CreateLogger<MsalIdentityProvider>())
        : null;

// Feature: derive default Windows identity from the OS username. NOTIFY_USER_ID is no
// longer read or required by the Windows head — EnvironmentIdentityProvider (which
// requires it) remains exclusively the console/dev host's identity source.
IIdentityProvider identity = (IIdentityProvider?)msalIdentity ?? new WindowsUsernameIdentityProvider(
    settings.DeviceId,
    loggerFactory.CreateLogger<WindowsUsernameIdentityProvider>());

var authProvider = NatsAuthSelection.Select(
    settings.NatsAuthServiceUrl,
    settings.NatsAuthServiceScope,
    settings.NatsCredsFile,
    msalIdentity,
    new HttpClient(),
    loggerFactory.CreateLogger("NatsAuthSelection"));

if (startupLogger.IsEnabled(LogLevel.Information))
{
    startupLogger.StartupConfigurationResolved(NatsUrlRedactor.Redact(options.NatsUrl), options.SubjectTemplate);
}

Application.Run(new TrayApplicationContext(
    ct => AgentHost.StartAsync(options, identity, new WindowsToastRenderer(), authProvider, ct),
    loggerFactory.CreateLogger<TrayApplicationContext>()));

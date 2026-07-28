// src/NotificationAgent.Windows/Logging.cs
using Microsoft.Extensions.Logging;

namespace NotificationAgent.Windows;

/// <summary>Source-generated <see cref="ILogger"/> extension methods for the Windows head
/// (feature: structured logging). Centralizing every message here (rather than calling the
/// `LogDebug`/`LogInformation`/`LogError` extensions directly at each call site) satisfies
/// CA1848/CA1873 — the source generator produces an `IsEnabled` check and avoids boxing or
/// evaluating message arguments when the level is disabled — and keeps message text/event
/// IDs in one place.</summary>
internal static partial class Log
{
    [LoggerMessage(EventId = 1, Level = LogLevel.Debug, Message = "Identity: mode = AAD/MSAL (client id {ClientId}).")]
    public static partial void IdentityModeAad(this ILogger logger, string clientId);

    [LoggerMessage(EventId = 2, Level = LogLevel.Information, Message = "Identity resolved via AAD/MSAL (deviceId={DeviceId}).")]
    public static partial void IdentityResolvedAad(this ILogger logger, string deviceId);

    [LoggerMessage(EventId = 3, Level = LogLevel.Debug, Message = "Silent MSAL token acquisition needs UI; falling back to interactive sign-in.")]
    public static partial void MsalInteractiveFallback(this ILogger logger);

    [LoggerMessage(EventId = 4, Level = LogLevel.Debug, Message = "Identity: mode = Windows username (NOTIFY_AAD_CLIENT_ID not set).")]
    public static partial void IdentityModeWindowsUsername(this ILogger logger);

    [LoggerMessage(EventId = 5, Level = LogLevel.Information, Message = "Identity resolved from Windows username (userId={UserId}, deviceId={DeviceId}).")]
    public static partial void IdentityResolvedWindowsUsername(this ILogger logger, string userId, string deviceId);

    [LoggerMessage(EventId = 6, Level = LogLevel.Debug, Message = "NATS auth: mode = external-auth-service ({AuthServiceUrl}).")]
    public static partial void NatsAuthModeExternalService(this ILogger logger, string authServiceUrl);

    [LoggerMessage(EventId = 7, Level = LogLevel.Debug, Message = "NATS auth: mode = creds-file ({CredsFile}).")]
    public static partial void NatsAuthModeCredsFile(this ILogger logger, string credsFile);

    [LoggerMessage(EventId = 8, Level = LogLevel.Debug, Message = "NATS auth: mode = none (unauthenticated).")]
    public static partial void NatsAuthModeNone(this ILogger logger);

    [LoggerMessage(EventId = 9, Level = LogLevel.Information, Message = "Agent started successfully; subscribed to subject {Subject}.")]
    public static partial void AgentStarted(this ILogger logger, string subject);

    [LoggerMessage(EventId = 10, Level = LogLevel.Error, Message = "Agent failed to start.")]
    public static partial void AgentStartFailed(this ILogger logger, Exception exception);

    [LoggerMessage(EventId = 11, Level = LogLevel.Warning, Message = "Failed to read settings file at {Path}; falling back to environment/default configuration.")]
    public static partial void SettingsFileReadFailed(this ILogger logger, Exception exception, string path);

    [LoggerMessage(EventId = 12, Level = LogLevel.Warning, Message = "Unrecognized log level '{LogLevelText}'; defaulting to Information.")]
    public static partial void LogLevelUnrecognized(this ILogger logger, string logLevelText);

    [LoggerMessage(EventId = 13, Level = LogLevel.Debug, Message = "Windows head starting: settings file = {SettingsPath} (exists={Exists}).")]
    public static partial void StartupSettingsFile(this ILogger logger, string settingsPath, bool exists);

    [LoggerMessage(EventId = 14, Level = LogLevel.Information, Message = "Startup configuration resolved: NATS = {NatsUrl}, subject template = {SubjectTemplate}.")]
    public static partial void StartupConfigurationResolved(this ILogger logger, string natsUrl, string subjectTemplate);
}

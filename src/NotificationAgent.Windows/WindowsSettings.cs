// src/NotificationAgent.Windows/WindowsSettings.cs
using System.Text.Json;
using Microsoft.Extensions.Logging;
using NotificationAgent.Core.Hosting;

namespace NotificationAgent.Windows;

/// <summary>Optional per-field overrides for the Windows head, read from
/// %LOCALAPPDATA%\DesktopNotificationAgent\settings.json (feature: app settings file).
/// Every field is optional; a null/blank value falls through to the environment variable
/// or built-in default. See <see cref="WindowsSettings"/> for the precedence rule.</summary>
public sealed class WindowsSettingsFile
{
    public string? NatsUrl
    {
        get; init;
    }

    public string? SubjectTemplate
    {
        get; init;
    }

    public string? AckSubject
    {
        get; init;
    }

    public string? NatsCredsFile
    {
        get; init;
    }

    public string? NatsAuthServiceUrl
    {
        get; init;
    }

    public string? NatsAuthServiceScope
    {
        get; init;
    }

    public string? AadClientId
    {
        get; init;
    }

    public string? AadTenantId
    {
        get; init;
    }

    public string? DeviceId
    {
        get; init;
    }

    public string? LogLevel
    {
        get; init;
    }
}

/// <summary>The fully resolved Windows head configuration: settings-file values layered
/// under environment variables, with built-in defaults for anything still unset (feature:
/// app settings file).</summary>
public sealed record ResolvedWindowsSettings(
    AgentOptions Options,
    string? NatsCredsFile,
    string? NatsAuthServiceUrl,
    string? NatsAuthServiceScope,
    string? AadClientId,
    string AadTenantId,
    string? DeviceId,
    LogLevel LogLevel);

/// <summary>A diagnostic produced while loading/resolving the settings file, deferred until a
/// real logger exists (feature: app settings file / logging). <see cref="LoadFile"/> and
/// <see cref="WindowsSettings.Resolve"/> run before the logger can be constructed — the
/// logger's minimum level is itself read from the settings file, a chicken-and-egg problem —
/// so passing a logger to them directly means messages like "settings file read failed" can
/// never actually surface. Instead, callers pass a collection to gather these into, then
/// replay each one through the real logger immediately after constructing it.</summary>
public abstract record SettingsDiagnostic
{
    private SettingsDiagnostic()
    {
    }

    /// <summary>Logs this diagnostic through <paramref name="logger"/> at the message's
    /// intended level.</summary>
    public abstract void Replay(ILogger logger);

    /// <summary>The settings file existed but failed to read/parse; the caller fell back to
    /// all-defaults for the whole file.</summary>
    public sealed record FileReadFailed(Exception Exception, string Path) : SettingsDiagnostic
    {
        public override void Replay(ILogger logger) => logger.SettingsFileReadFailed(Exception, Path);
    }

    /// <summary>The resolved log level text (from env var or settings file) didn't parse to a
    /// defined <see cref="Microsoft.Extensions.Logging.LogLevel"/> value; the caller fell back
    /// to <see cref="Microsoft.Extensions.Logging.LogLevel.Information"/>.</summary>
    public sealed record LogLevelUnrecognized(string LogLevelText) : SettingsDiagnostic
    {
        public override void Replay(ILogger logger) => logger.LogLevelUnrecognized(LogLevelText);
    }
}

/// <summary>Loads the optional Windows settings file and resolves it against environment
/// variables and built-in defaults (feature: app settings file). Precedence per field is
/// environment variable (non-blank) &gt; settings file value (non-blank) &gt; built-in
/// default. A missing file is normal — a fresh install, or an operator relying purely on
/// environment variables — and is never created or required. A malformed file logs a
/// warning and is treated as all-defaults so startup never fails because of it.</summary>
public static class WindowsSettings
{
    private static readonly JsonSerializerOptions JsonOptions = new()
    {
        PropertyNameCaseInsensitive = true,
    };

    public static string DefaultPath => Path.Combine(
        Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),
        "DesktopNotificationAgent",
        "settings.json");

    /// <summary>Reads and parses the settings file at <paramref name="path"/>. Returns an
    /// all-null instance when the file is absent, empty, or fails to parse. A read/parse
    /// failure is appended to <paramref name="diagnostics"/> (if provided) rather than logged
    /// immediately — see <see cref="SettingsDiagnostic"/> for why.</summary>
    public static WindowsSettingsFile LoadFile(string path, ICollection<SettingsDiagnostic>? diagnostics = null)
    {
        if (!File.Exists(path))
        {
            return new WindowsSettingsFile();
        }

        try
        {
            var json = File.ReadAllText(path);
            return JsonSerializer.Deserialize<WindowsSettingsFile>(json, JsonOptions) ?? new WindowsSettingsFile();
        }
        catch (Exception ex) when (ex is JsonException or IOException or UnauthorizedAccessException)
        {
            diagnostics?.Add(new SettingsDiagnostic.FileReadFailed(ex, path));
            return new WindowsSettingsFile();
        }
    }

    /// <summary>Applies env-var &gt; file &gt; default precedence to every field, producing
    /// the concrete configuration <c>Program.cs</c> passes to <c>AgentHost</c> and the
    /// identity/NATS-auth selection helpers. <paramref name="getEnv"/> is injected so this
    /// logic is testable without touching real process environment variables. An
    /// unrecognized log level is appended to <paramref name="diagnostics"/> (if provided)
    /// rather than logged immediately — see <see cref="SettingsDiagnostic"/> for why.</summary>
    public static ResolvedWindowsSettings Resolve(
        WindowsSettingsFile file, Func<string, string?> getEnv, ICollection<SettingsDiagnostic>? diagnostics = null)
    {
        var options = new AgentOptions
        {
            NatsUrl = Pick(getEnv("NOTIFY_NATS_URL"), file.NatsUrl, "nats://127.0.0.1:4222"),
            SubjectTemplate = Pick(getEnv("NOTIFY_SUBJECT_TEMPLATE"), file.SubjectTemplate, "notify.user.{0}.desktop"),
            AckSubject = Pick(getEnv("NOTIFY_ACK_SUBJECT"), file.AckSubject, "notify.ack.desktop"),
        };

        var logLevelText = Pick(getEnv("NOTIFY_LOG_LEVEL"), file.LogLevel, "Information");
        if (!Enum.TryParse<LogLevel>(logLevelText, ignoreCase: true, out var logLevel))
        {
            diagnostics?.Add(new SettingsDiagnostic.LogLevelUnrecognized(logLevelText));
            logLevel = LogLevel.Information;
        }

        return new ResolvedWindowsSettings(
            options,
            NatsCredsFile: PickOptional(getEnv("NOTIFY_NATS_CREDS_FILE"), file.NatsCredsFile),
            NatsAuthServiceUrl: PickOptional(getEnv("NOTIFY_NATS_AUTH_SERVICE_URL"), file.NatsAuthServiceUrl),
            NatsAuthServiceScope: PickOptional(getEnv("NOTIFY_NATS_AUTH_SERVICE_SCOPE"), file.NatsAuthServiceScope),
            AadClientId: PickOptional(getEnv("NOTIFY_AAD_CLIENT_ID"), file.AadClientId),
            AadTenantId: Pick(getEnv("NOTIFY_AAD_TENANT_ID"), file.AadTenantId, "organizations"),
            DeviceId: PickOptional(getEnv("NOTIFY_DEVICE_ID"), file.DeviceId),
            LogLevel: logLevel);
    }

    // Trim before checking blankness (not after): a whitespace-only value must be treated as
    // unset so it correctly falls through to the next tier of precedence, rather than
    // "winning" with an empty string.
    private static string Pick(string? env, string? fileValue, string fallback) =>
        PickOptional(env, fileValue) ?? fallback;

    private static string? PickOptional(string? env, string? fileValue)
    {
        var trimmedEnv = env?.Trim();
        if (trimmedEnv is { Length: > 0 })
        {
            return trimmedEnv;
        }

        var trimmedFile = fileValue?.Trim();
        return trimmedFile is { Length: > 0 } ? trimmedFile : null;
    }
}

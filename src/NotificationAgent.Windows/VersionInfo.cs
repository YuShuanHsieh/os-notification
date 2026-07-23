namespace NotificationAgent.Windows;

/// <summary>Formats the running assembly's version for the tray menu (design: system tray icon).</summary>
internal static class VersionInfo
{
    internal static string Current => Format(typeof(VersionInfo).Assembly.GetName().Version);

    internal static string Format(Version? version) =>
        version is null ? "unknown" : $"{version.Major}.{version.Minor}.{version.Build}";
}

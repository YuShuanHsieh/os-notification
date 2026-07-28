using Microsoft.Extensions.Logging;
using NotificationAgent.Windows;
using Xunit;

namespace NotificationAgent.Windows.Tests;

public class WindowsSettingsTests
{
    private static readonly Func<string, string?> NoEnv = _ => null;

    [Fact]
    public void LoadFile_returns_all_defaults_when_file_is_absent()
    {
        var path = Path.Combine(Path.GetTempPath(), $"settings-{Guid.NewGuid():N}.json");

        var file = WindowsSettings.LoadFile(path);

        Assert.Null(file.NatsUrl);
        Assert.Null(file.SubjectTemplate);
        Assert.Null(file.AckSubject);
        Assert.Null(file.NatsCredsFile);
        Assert.Null(file.NatsAuthServiceUrl);
        Assert.Null(file.NatsAuthServiceScope);
        Assert.Null(file.AadClientId);
        Assert.Null(file.AadTenantId);
        Assert.Null(file.DeviceId);
        Assert.Null(file.LogLevel);
    }

    [Fact]
    public void Resolve_uses_built_in_defaults_when_file_absent_and_no_env()
    {
        var resolved = WindowsSettings.Resolve(new WindowsSettingsFile(), NoEnv);

        Assert.Equal("nats://127.0.0.1:4222", resolved.Options.NatsUrl);
        Assert.Equal("notify.user.{0}.desktop", resolved.Options.SubjectTemplate);
        Assert.Equal("notify.ack.desktop", resolved.Options.AckSubject);
        Assert.Null(resolved.NatsCredsFile);
        Assert.Null(resolved.NatsAuthServiceUrl);
        Assert.Null(resolved.NatsAuthServiceScope);
        Assert.Null(resolved.AadClientId);
        Assert.Equal("organizations", resolved.AadTenantId);
        Assert.Null(resolved.DeviceId);
        Assert.Equal(LogLevel.Information, resolved.LogLevel);
    }

    [Fact]
    public void LoadFile_parses_present_fields_from_a_valid_file()
    {
        var path = WriteTempFile(
            """
            {
              "natsUrl": "nats://custom:4222",
              "aadClientId": "abc-123"
            }
            """);
        try
        {
            var file = WindowsSettings.LoadFile(path);

            Assert.Equal("nats://custom:4222", file.NatsUrl);
            Assert.Equal("abc-123", file.AadClientId);
            Assert.Null(file.SubjectTemplate);
            Assert.Null(file.AckSubject);
            Assert.Null(file.DeviceId);
        }
        finally
        {
            File.Delete(path);
        }
    }

    [Fact]
    public void Resolve_uses_file_value_when_env_not_set_and_defaults_for_the_rest()
    {
        var file = new WindowsSettingsFile { NatsUrl = "nats://custom:4222", AadClientId = "abc-123" };

        var resolved = WindowsSettings.Resolve(file, NoEnv);

        Assert.Equal("nats://custom:4222", resolved.Options.NatsUrl);
        Assert.Equal("abc-123", resolved.AadClientId);
        Assert.Equal("notify.user.{0}.desktop", resolved.Options.SubjectTemplate);
        Assert.Equal("organizations", resolved.AadTenantId);
    }

    [Fact]
    public void Resolve_prefers_env_var_over_file_value()
    {
        var file = new WindowsSettingsFile { NatsUrl = "nats://from-file:4222" };
        Func<string, string?> env = name => name == "NOTIFY_NATS_URL" ? "nats://from-env:4222" : null;

        var resolved = WindowsSettings.Resolve(file, env);

        Assert.Equal("nats://from-env:4222", resolved.Options.NatsUrl);
    }

    [Fact]
    public void Resolve_treats_blank_env_var_as_unset_and_falls_back_to_file()
    {
        var file = new WindowsSettingsFile { NatsUrl = "nats://from-file:4222" };
        Func<string, string?> env = name => name == "NOTIFY_NATS_URL" ? "   " : null;

        var resolved = WindowsSettings.Resolve(file, env);

        Assert.Equal("nats://from-file:4222", resolved.Options.NatsUrl);
    }

    [Fact]
    public void Resolve_parses_log_level_case_insensitively()
    {
        var file = new WindowsSettingsFile { LogLevel = "debug" };

        var resolved = WindowsSettings.Resolve(file, NoEnv);

        Assert.Equal(LogLevel.Debug, resolved.LogLevel);
    }

    [Fact]
    public void Resolve_falls_back_to_information_when_log_level_is_unrecognized()
    {
        var file = new WindowsSettingsFile { LogLevel = "not-a-level" };

        var resolved = WindowsSettings.Resolve(file, NoEnv);

        Assert.Equal(LogLevel.Information, resolved.LogLevel);
    }

    [Fact]
    public void LoadFile_falls_back_to_defaults_without_throwing_on_malformed_json()
    {
        var path = WriteTempFile("{ this is not valid json");
        try
        {
            var file = WindowsSettings.LoadFile(path);

            Assert.Null(file.NatsUrl);
            Assert.Null(file.AadClientId);
        }
        finally
        {
            File.Delete(path);
        }
    }

    [Fact]
    public void LoadFile_falls_back_to_defaults_without_throwing_when_json_is_the_wrong_shape()
    {
        var path = WriteTempFile("[1, 2, 3]");
        try
        {
            var file = WindowsSettings.LoadFile(path);

            Assert.Null(file.NatsUrl);
        }
        finally
        {
            File.Delete(path);
        }
    }

    private static string WriteTempFile(string contents)
    {
        var path = Path.Combine(Path.GetTempPath(), $"settings-{Guid.NewGuid():N}.json");
        File.WriteAllText(path, contents);
        return path;
    }
}

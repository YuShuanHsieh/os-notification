using NotificationAgent.Core.Nats;
using NotificationAgent.Windows;
using Xunit;

namespace NotificationAgent.Windows.Tests;

public class NatsAuthSelectionTests
{
    private static readonly HttpClient Http = new();

    [Fact]
    public void Select_returns_null_when_nothing_configured()
    {
        var result = NatsAuthSelection.Select(
            authServiceUrl: null, authServiceScope: null, credsFile: null, msalIdentity: null, Http);

        Assert.Null(result);
    }

    [Fact]
    public void Select_returns_creds_file_provider_when_only_creds_file_set()
    {
        var result = NatsAuthSelection.Select(
            authServiceUrl: null, authServiceScope: null, credsFile: "/x.creds", msalIdentity: null, Http);

        var provider = Assert.IsType<CredsFileNatsAuthProvider>(result);
        Assert.Equal("/x.creds", provider.GetAuthOpts().CredsFile);
    }

    [Fact]
    public void Select_throws_when_auth_service_url_set_without_msal_identity()
    {
        Assert.Throws<InvalidOperationException>(() => NatsAuthSelection.Select(
            authServiceUrl: "https://auth.example.com",
            authServiceScope: "api://x/Nats.Connect",
            credsFile: null,
            msalIdentity: null,
            Http));
    }

    [Fact]
    public void Select_throws_when_auth_service_url_set_without_scope()
    {
        var msal = new MsalIdentityProvider("client-id", "tenant-id");

        Assert.Throws<InvalidOperationException>(() => NatsAuthSelection.Select(
            authServiceUrl: "https://auth.example.com",
            authServiceScope: null,
            credsFile: null,
            msalIdentity: msal,
            Http));
    }

    [Fact]
    public void Select_returns_external_auth_service_provider_when_fully_configured()
    {
        var msal = new MsalIdentityProvider("client-id", "tenant-id");

        var result = NatsAuthSelection.Select(
            authServiceUrl: "https://auth.example.com",
            authServiceScope: "api://x/Nats.Connect",
            credsFile: null,
            msalIdentity: msal,
            Http);

        Assert.IsType<ExternalAuthServiceNatsAuthProvider>(result);
    }
}

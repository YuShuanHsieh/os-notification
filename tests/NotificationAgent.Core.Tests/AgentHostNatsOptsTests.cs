using NATS.Client.Core;
using NotificationAgent.Core.Hosting;
using NotificationAgent.Core.Nats;
using Xunit;

namespace NotificationAgent.Core.Tests;

public class AgentHostNatsOptsTests
{
    private sealed class StubAuthProvider : INatsAuthProvider
    {
        private readonly NatsAuthOpts _opts;

        public StubAuthProvider(NatsAuthOpts opts) => _opts = opts;

        public NatsAuthOpts GetAuthOpts() => _opts;
    }

    [Fact]
    public void BuildNatsOpts_without_provider_leaves_auth_unset()
    {
        var options = new AgentOptions { NatsUrl = "nats://127.0.0.1:4222" };

        var opts = AgentHost.BuildNatsOpts(options, authProvider: null);

        Assert.Equal("nats://127.0.0.1:4222", opts.Url);
        Assert.Null(opts.AuthOpts.CredsFile);
    }

    [Fact]
    public void BuildNatsOpts_with_provider_applies_its_auth_opts()
    {
        var options = new AgentOptions { NatsUrl = "wss://nats.example.com/ws" };
        var provider = new StubAuthProvider(new NatsAuthOpts { CredsFile = "/x.creds" });

        var opts = AgentHost.BuildNatsOpts(options, provider);

        Assert.Equal("wss://nats.example.com/ws", opts.Url);
        Assert.Equal("/x.creds", opts.AuthOpts.CredsFile);
    }
}

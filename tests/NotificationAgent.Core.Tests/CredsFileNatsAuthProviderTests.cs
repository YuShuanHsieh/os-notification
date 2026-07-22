using NotificationAgent.Core.Nats;
using Xunit;

namespace NotificationAgent.Core.Tests;

public class CredsFileNatsAuthProviderTests
{
    [Fact]
    public void GetAuthOpts_sets_creds_file_path()
    {
        var provider = new CredsFileNatsAuthProvider("/etc/nats/user.creds");

        var opts = provider.GetAuthOpts();

        Assert.Equal("/etc/nats/user.creds", opts.CredsFile);
    }
}

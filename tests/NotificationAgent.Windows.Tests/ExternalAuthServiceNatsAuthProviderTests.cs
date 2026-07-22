using System.Net;
using System.Net.Http.Json;
using NATS.Client.Core;
using NotificationAgent.Windows;
using Xunit;

namespace NotificationAgent.Windows.Tests;

public class ExternalAuthServiceNatsAuthProviderTests
{
    private sealed class StubHandler : HttpMessageHandler
    {
        private readonly Func<HttpRequestMessage, HttpResponseMessage> _respond;

        public StubHandler(Func<HttpRequestMessage, HttpResponseMessage> respond) => _respond = respond;

        public HttpRequestMessage? LastRequest
        {
            get; private set;
        }

        public string? LastBody
        {
            get; private set;
        }

        protected override async Task<HttpResponseMessage> SendAsync(
            HttpRequestMessage request, CancellationToken cancellationToken)
        {
            LastRequest = request;
            LastBody = request.Content is null
                ? null
                : await request.Content.ReadAsStringAsync(cancellationToken);
            return _respond(request);
        }
    }

    private static HttpResponseMessage JsonResponse(HttpStatusCode status, object body) =>
        new(status)
        {
            Content = JsonContent.Create(body),
        };

    private static ExternalAuthServiceNatsAuthProvider CreateProvider(
        StubHandler handler, Func<CancellationToken, Task<string>>? accessTokenProvider = null) =>
        new(
            new Uri("https://auth.example.com/nats-jwt"),
            accessTokenProvider ?? (_ => Task.FromResult("aad-token")),
            new HttpClient(handler));

    [Fact]
    public async Task Callback_posts_public_key_and_bearer_token_then_returns_cred()
    {
        var handler = new StubHandler(_ => JsonResponse(HttpStatusCode.OK, new { jwt = "jwt-1" }));
        var provider = CreateProvider(handler, _ => Task.FromResult("aad-token-1"));

        var cred = await provider.GetAuthOpts().AuthCredCallback!(
            new Uri("nats://server"), CancellationToken.None);

        Assert.Equal(HttpMethod.Post, handler.LastRequest!.Method);
        Assert.Equal("Bearer", handler.LastRequest.Headers.Authorization!.Scheme);
        Assert.Equal("aad-token-1", handler.LastRequest.Headers.Authorization.Parameter);
        Assert.Contains(provider.PublicKey, handler.LastBody);
        Assert.Equal(NatsAuthCred.FromJwt("jwt-1", provider.Seed), cred);
    }

    [Fact]
    public async Task Callback_reuses_the_same_nkey_seed_across_calls()
    {
        var handler = new StubHandler(_ => JsonResponse(HttpStatusCode.OK, new { jwt = "same-jwt" }));
        var provider = CreateProvider(handler);
        var callback = provider.GetAuthOpts().AuthCredCallback!;

        var first = await callback(new Uri("nats://server"), CancellationToken.None);
        var second = await callback(new Uri("nats://server"), CancellationToken.None);

        Assert.Equal(first, second);
    }

    [Fact]
    public async Task Callback_throws_on_non_success_status()
    {
        var handler = new StubHandler(_ => new HttpResponseMessage(HttpStatusCode.Unauthorized));
        var provider = CreateProvider(handler);

        await Assert.ThrowsAsync<InvalidOperationException>(() =>
            provider.GetAuthOpts().AuthCredCallback!(new Uri("nats://server"), CancellationToken.None).AsTask());
    }

    [Fact]
    public async Task Callback_throws_when_response_has_no_jwt()
    {
        var handler = new StubHandler(_ => JsonResponse(HttpStatusCode.OK, new { }));
        var provider = CreateProvider(handler);

        await Assert.ThrowsAsync<InvalidOperationException>(() =>
            provider.GetAuthOpts().AuthCredCallback!(new Uri("nats://server"), CancellationToken.None).AsTask());
    }
}

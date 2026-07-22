using System.Net.Http.Headers;
using System.Net.Http.Json;
using NATS.Client.Core;
using NATS.NKeys;
using NotificationAgent.Core.Nats;

namespace NotificationAgent.Windows;

/// <summary>Authenticates NATS via an external HTTPS auth service, reusing the agent's AAD
/// identity (design §4). The NATS JWT is refreshed on every connect/reconnect via
/// AuthCredCallback, which NATS.Client.Core re-invokes automatically.
/// The NKey seed is generated once per process and never leaves the device;
/// only its public key is sent to the auth service.</summary>
public sealed class ExternalAuthServiceNatsAuthProvider : INatsAuthProvider
{
    private readonly Uri _authServiceUrl;
    private readonly Func<CancellationToken, Task<string>> _accessTokenProvider;
    private readonly HttpClient _httpClient;

    public ExternalAuthServiceNatsAuthProvider(
        Uri authServiceUrl,
        Func<CancellationToken, Task<string>> accessTokenProvider,
        HttpClient httpClient)
    {
        _authServiceUrl = authServiceUrl;
        _accessTokenProvider = accessTokenProvider;
        _httpClient = httpClient;

        using var keyPair = KeyPair.CreatePair(PrefixByte.User);
        Seed = keyPair.GetSeed();
        PublicKey = keyPair.GetPublicKey();
    }

    public string PublicKey
    {
        get;
    }

    internal string Seed
    {
        get;
    }

    public NatsAuthOpts GetAuthOpts() => new()
    {
        AuthCredCallback = async (_, ct) =>
        {
            var aadToken = await _accessTokenProvider(ct).ConfigureAwait(false);
            var jwt = await FetchJwtAsync(aadToken, ct).ConfigureAwait(false);
            return NatsAuthCred.FromJwt(jwt, Seed);
        },
    };

    private async Task<string> FetchJwtAsync(string aadToken, CancellationToken ct)
    {
        using var request = new HttpRequestMessage(HttpMethod.Post, _authServiceUrl)
        {
            Content = JsonContent.Create(new { nkeyPublicKey = PublicKey }),
        };
        request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", aadToken);

        using var response = await _httpClient.SendAsync(request, ct).ConfigureAwait(false);
        if (!response.IsSuccessStatusCode)
        {
            throw new InvalidOperationException(
                $"NATS auth service returned {(int)response.StatusCode} {response.ReasonPhrase}.");
        }

        var body = await response.Content.ReadFromJsonAsync<AuthServiceResponse>(ct).ConfigureAwait(false);
        if (body?.Jwt is not { Length: > 0 })
        {
            throw new InvalidOperationException("NATS auth service response did not contain a jwt.");
        }

        return body.Jwt;
    }

    private sealed class AuthServiceResponse
    {
        public string? Jwt
        {
            get; set;
        }
    }
}

# Offline-capable .NET build image. See docker/README.md for the refresh
# (needs network) vs. offline build/test (no network) workflow.
FROM mcr.microsoft.com/dotnet/sdk:10.0

WORKDIR /repo
COPY . .

# dotnet restore --locked-mode silently regenerates a missing
# packages.lock.json (exit 0, no error), so a committed lock file that was
# deleted would go undetected. Guard for that case explicitly, before any
# restore runs.
RUN for f in \
        src/NotificationAgent.Core/packages.lock.json \
        src/NotificationAgent.ConsoleHost/packages.lock.json \
        src/NotificationAgent.Windows/packages.lock.json \
        tests/NotificationAgent.Core.Tests/packages.lock.json \
        tests/NotificationAgent.Windows.Tests/packages.lock.json \
        tools/TestPublisher/packages.lock.json; \
    do \
        test -f "$f" || { echo "missing lock file: $f" >&2; exit 1; }; \
    done

# All three restores need network here (docker build time) to reach nuget.org.
# --locked-mode fails the build if a committed packages.lock.json is stale
# (its content hash doesn't match the referenced packages; surfaces as
# NU1403), so drift surfaces immediately. It does NOT catch a fully missing
# lock file (NuGet just regenerates one and restore succeeds) -- that case is
# covered by the guard step above.
RUN dotnet restore NotificationAgent.sln --locked-mode
RUN dotnet restore src/NotificationAgent.Windows/NotificationAgent.Windows.csproj --locked-mode
RUN dotnet restore tests/NotificationAgent.Windows.Tests/NotificationAgent.Windows.Tests.csproj --locked-mode

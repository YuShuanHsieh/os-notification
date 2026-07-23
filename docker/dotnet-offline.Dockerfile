# Offline-capable .NET build image. See docker/README.md for the refresh
# (needs network) vs. offline build/test (no network) workflow.
FROM mcr.microsoft.com/dotnet/sdk:10.0

WORKDIR /repo
COPY . .

# All three restores need network here (docker build time) to reach nuget.org.
# --locked-mode fails the build if packages.lock.json is missing or doesn't
# match the referenced packages, so a stale lock file surfaces immediately.
RUN dotnet restore NotificationAgent.sln --locked-mode
RUN dotnet restore src/NotificationAgent.Windows/NotificationAgent.Windows.csproj --locked-mode
RUN dotnet restore tests/NotificationAgent.Windows.Tests/NotificationAgent.Windows.Tests.csproj --locked-mode

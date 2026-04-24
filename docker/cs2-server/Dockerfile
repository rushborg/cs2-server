# ─── Stage 1: build the RushBChatForwarder CSSharp plugin ───────────────
#
# We compile against the same CounterStrikeSharp API version that
# entrypoint.sh downloads into the container (see CSSHARP_URL). Only the
# plugin DLL is shipped; CSS API assemblies stay resolved at runtime from
# the server's own addons/counterstrikesharp dir so we don't duplicate
# them and break CSS internals.
FROM mcr.microsoft.com/dotnet/sdk:8.0 AS plugin-builder
WORKDIR /src
COPY plugins/RushBChatForwarder/ .
RUN dotnet restore && \
    dotnet publish -c Release -o /out --no-self-contained

# ─── Stage 2: CS2 runtime ───────────────────────────────────────────────
FROM ubuntu:22.04

ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update -qq && \
    apt-get install -y -qq \
        lib32gcc-s1 curl unzip libicu-dev gosu ca-certificates locales && \
    locale-gen en_US.UTF-8 && \
    rm -rf /var/lib/apt/lists/*

# Create steam user (uid 1000)
RUN useradd -m -u 1000 -s /bin/bash steam

VOLUME ["/home/steam/cs2-dedicated", "/instance/config", "/instance/data", "/shared", "/custom/plugins", "/custom/maps", "/demos"]

COPY entrypoint.sh /rushborg-entrypoint.sh
COPY lang_ru.json /rushborg-lang_ru.json
COPY --from=plugin-builder /out/RushBChatForwarder.dll /rushborg-plugins/RushBChatForwarder/RushBChatForwarder.dll
RUN chmod +x /rushborg-entrypoint.sh

ENTRYPOINT ["/rushborg-entrypoint.sh"]

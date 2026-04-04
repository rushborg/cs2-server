FROM ubuntu:22.04

# CS2 is pre-installed by the agent via SteamCMD on the host.
# This image only needs runtime dependencies for CS2 + plugins.
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
RUN chmod +x /rushborg-entrypoint.sh

ENTRYPOINT ["/rushborg-entrypoint.sh"]

FROM ubuntu:22.04

# Install dependencies + SteamCMD from apt (faster downloads than cm2network)
ENV DEBIAN_FRONTEND=noninteractive
RUN dpkg --add-architecture i386 && \
    apt-get update -qq && \
    echo steam steam/question select "I AGREE" | debconf-set-selections && \
    echo steam steam/license note '' | debconf-set-selections && \
    apt-get install -y -qq \
        lib32gcc-s1 steamcmd \
        curl unzip libicu-dev gosu ca-certificates && \
    rm -rf /var/lib/apt/lists/*

# Create steam user (same as cm2network — uid 1000)
RUN useradd -m -u 1000 -s /bin/bash steam && \
    mkdir -p /home/steam/.steam && \
    ln -sf /home/steam/.local/share/Steam /home/steam/.steam/root && \
    ln -sf /home/steam/.local/share/Steam /home/steam/.steam/steam && \
    mkdir -p /home/steam/.local/share/Steam && \
    chown -R steam:steam /home/steam

VOLUME ["/home/steam/cs2-dedicated", "/instance/config", "/instance/data", "/shared", "/custom/plugins", "/custom/maps", "/demos"]

COPY entrypoint.sh /rushborg-entrypoint.sh
RUN chmod +x /rushborg-entrypoint.sh

ENTRYPOINT ["/rushborg-entrypoint.sh"]

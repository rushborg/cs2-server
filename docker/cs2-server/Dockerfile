FROM cm2network/steamcmd:root

# Use steamcmd base (always up-to-date SteamCMD)
# CS2 will be installed on first run into a persistent volume

USER root
RUN apt-get update -qq && apt-get install -y -qq curl unzip libicu-dev && rm -rf /var/lib/apt/lists/*
USER steam

VOLUME ["/home/steam/cs2-dedicated", "/instance/config", "/shared", "/custom/plugins", "/custom/maps", "/demos"]

COPY entrypoint.sh /rushborg-entrypoint.sh

USER root
RUN chmod +x /rushborg-entrypoint.sh
USER steam

ENTRYPOINT ["/rushborg-entrypoint.sh"]

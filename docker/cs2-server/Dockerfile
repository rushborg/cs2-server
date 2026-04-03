FROM cm2network/steamcmd:root

# CS2 base + plugins installed on first run into shared bind-mount volume.
# Entrypoint runs as root for setup (symlinks, permissions), then
# drops to steam user for CS2 process via exec gosu/su.

USER root
RUN apt-get update -qq && apt-get install -y -qq curl unzip libicu-dev gosu && rm -rf /var/lib/apt/lists/*

VOLUME ["/home/steam/cs2-dedicated", "/instance/config", "/instance/data", "/shared", "/custom/plugins", "/custom/maps", "/demos"]

COPY entrypoint.sh /rushborg-entrypoint.sh
RUN chmod +x /rushborg-entrypoint.sh

# Entrypoint runs as root, drops to steam for CS2
ENTRYPOINT ["/rushborg-entrypoint.sh"]

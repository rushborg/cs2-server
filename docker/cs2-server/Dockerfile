FROM cm2network/cs2

# cm2network/cs2:
# - Downloads CS2 on FIRST RUN (not during build)
# - Auto-updates CS2 on each restart
# - CS2 at /home/steam/cs2-dedicated/

USER root
RUN apt-get update -qq && apt-get install -y -qq curl && rm -rf /var/lib/apt/lists/*
USER steam

# Volume mount points
VOLUME ["/instance/config", "/shared", "/custom/plugins", "/custom/maps", "/demos"]

# Our entrypoint installs plugins on first run, copies configs, then starts CS2
COPY entrypoint.sh /rushborg-entrypoint.sh

USER root
RUN chmod +x /rushborg-entrypoint.sh
USER steam

ENTRYPOINT ["/rushborg-entrypoint.sh"]

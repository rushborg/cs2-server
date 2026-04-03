#!/bin/bash
# RUSH-B.ORG CS2 Server Entrypoint
# 1. Lets cm2network/cs2 install/update CS2
# 2. Installs MetaMod + SourceMod + get5 if not present
# 3. Copies configs, plugins, maps
# 4. Starts CS2

CS2_DIR=/home/steam/cs2-dedicated
CSGO_DIR="${CS2_DIR}/game/csgo"
PLUGIN_MARKER="${CSGO_DIR}/addons/.rushborg-plugins-installed"

# Plugin URLs (update these when new versions release)
METAMOD_URL="https://mms.alliedmods.net/mmsdrop/2.0/mmsource-2.0.0-git1313-linux.tar.gz"
SOURCEMOD_URL="https://sm.alliedmods.net/smdrop/1.12/sourcemod-1.12.0-git7178-linux.tar.gz"
GET5_URL="https://github.com/splewis/get5/releases/download/v0.15.0/get5_v0.15.0.tar.gz"

# ─── Wait for CS2 to be installed by base image ──────────
echo "[RUSH-B.ORG] Waiting for CS2 installation..."

# Run the base image's update script first (downloads/updates CS2)
if [ -f "${CS2_DIR}/entry.sh" ]; then
    # The base entry.sh runs steamcmd update then starts cs2
    # We need to run ONLY the update part, not the server start
    # So we'll check if csgo dir exists after a short wait
    :
fi

# If csgo dir doesn't exist yet, let the base image handle initial install
# by running its steamcmd update
if [ ! -d "${CSGO_DIR}" ]; then
    echo "[RUSH-B.ORG] CS2 not installed yet, running SteamCMD..."
    /home/steam/steamcmd/steamcmd.sh \
        +force_install_dir "${CS2_DIR}" \
        +login anonymous \
        +app_update 730 validate \
        +quit || true
    # Retry once (SteamCMD self-update)
    if [ ! -d "${CSGO_DIR}" ]; then
        /home/steam/steamcmd/steamcmd.sh \
            +force_install_dir "${CS2_DIR}" \
            +login anonymous \
            +app_update 730 validate \
            +quit || true
    fi
fi

# ─── Install plugins (once) ──────────────────────────────
if [ -d "${CSGO_DIR}" ] && [ ! -f "${PLUGIN_MARKER}" ]; then
    echo "[RUSH-B.ORG] Installing MetaMod..."
    curl -fsSL "${METAMOD_URL}" | tar xz -C "${CSGO_DIR}/" 2>/dev/null || echo "  MetaMod install failed"

    echo "[RUSH-B.ORG] Installing SourceMod..."
    curl -fsSL "${SOURCEMOD_URL}" | tar xz -C "${CSGO_DIR}/" 2>/dev/null || echo "  SourceMod install failed"

    echo "[RUSH-B.ORG] Installing get5..."
    curl -fsSL "${GET5_URL}" | tar xz -C "${CSGO_DIR}/" 2>/dev/null || echo "  get5 install failed"

    touch "${PLUGIN_MARKER}"
    echo "[RUSH-B.ORG] Plugins installed"
fi

# ─── Copy instance configs ───────────────────────────────
if [ -d /instance/config ] && [ -d "${CSGO_DIR}" ]; then
    mkdir -p "${CSGO_DIR}/cfg"
    cp -f /instance/config/*.cfg "${CSGO_DIR}/cfg/" 2>/dev/null || true
fi

# ─── Copy shared admins ─────────────────────────────────
if [ -f /shared/admins_simple.ini ] && [ -d "${CSGO_DIR}" ]; then
    mkdir -p "${CSGO_DIR}/addons/sourcemod/configs"
    cp -f /shared/admins_simple.ini "${CSGO_DIR}/addons/sourcemod/configs/admins_simple.ini"
fi

# ─── Copy custom plugins ────────────────────────────────
if [ -d /custom/plugins ] && [ -d "${CSGO_DIR}" ]; then
    mkdir -p "${CSGO_DIR}/addons/sourcemod/plugins"
    cp -f /custom/plugins/*.smx "${CSGO_DIR}/addons/sourcemod/plugins/" 2>/dev/null || true
    echo "[RUSH-B.ORG] Custom plugins loaded"
fi

# ─── Copy custom maps ───────────────────────────────────
if [ -d /custom/maps ] && [ -d "${CSGO_DIR}" ]; then
    mkdir -p "${CSGO_DIR}/maps"
    cp -f /custom/maps/*.bsp "${CSGO_DIR}/maps/" 2>/dev/null || true
    cp -f /custom/maps/*.nav "${CSGO_DIR}/maps/" 2>/dev/null || true
    echo "[RUSH-B.ORG] Custom maps loaded"
fi

echo "[RUSH-B.ORG] Starting CS2 server..."

# Set LD_LIBRARY_PATH for CS2 dependencies (libv8.so etc)
export LD_LIBRARY_PATH="${CS2_DIR}/game/bin/linuxsteamrt64:${LD_LIBRARY_PATH}"

exec "${CS2_DIR}/game/bin/linuxsteamrt64/cs2" -dedicated \
    -port "${CS2_PORT:-27015}" \
    +tv_port "${CS2_GOTV_PORT:-27020}" \
    +map "${CS2_MAP:-de_mirage}" \
    +game_type 0 +game_mode 1 \
    -usercon

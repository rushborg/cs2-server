#!/bin/bash
# RUSH-B.ORG CS2 Server Entrypoint
# Stack: MetaMod + CounterStrikeSharp + MatchZy (get5-compatible)

CS2_DIR=/home/steam/cs2-dedicated
CSGO_DIR="${CS2_DIR}/game/csgo"
PLUGIN_MARKER="${CSGO_DIR}/addons/.rushborg-plugins-installed"

# Plugin URLs — update these when new versions release
METAMOD_URL="https://mms.alliedmods.net/mmsdrop/2.0/mmsource-2.0.0-git1313-linux.tar.gz"
MATCHZY_URL="https://github.com/shobhit-pathak/MatchZy/releases/download/0.8.15/MatchZy-0.8.15-with-cssharp-linux.zip"

# ─── Install/update CS2 ──────────────────────────────────
if [ ! -f "${CS2_DIR}/game/bin/linuxsteamrt64/cs2" ]; then
    echo "[RUSH-B.ORG] CS2 not installed, running SteamCMD..."
    /home/steam/steamcmd/steamcmd.sh \
        +force_install_dir "${CS2_DIR}" \
        +login anonymous \
        +app_update 730 validate \
        +quit || true
    # Retry once (SteamCMD self-update)
    if [ ! -f "${CS2_DIR}/game/bin/linuxsteamrt64/cs2" ]; then
        /home/steam/steamcmd/steamcmd.sh \
            +force_install_dir "${CS2_DIR}" \
            +login anonymous \
            +app_update 730 validate \
            +quit || true
    fi
fi

# ─── Fix steamclient.so (SteamCMD version → CS2 bin) ─────
if [ -f "/home/steam/steamcmd/linux64/steamclient.so" ] && [ -d "${CS2_DIR}/game/bin/linuxsteamrt64/" ]; then
    cp -f /home/steam/steamcmd/linux64/steamclient.so "${CS2_DIR}/game/bin/linuxsteamrt64/steamclient.so" 2>/dev/null || true
    echo "[RUSH-B.ORG] steamclient.so updated"
fi

# ─── Install plugins (once) ──────────────────────────────
if [ -d "${CSGO_DIR}" ] && [ ! -f "${PLUGIN_MARKER}" ]; then
    echo "[RUSH-B.ORG] Installing MetaMod..."
    curl -fsSL "${METAMOD_URL}" | tar xz -C "${CSGO_DIR}/" 2>/dev/null || echo "  MetaMod install failed"

    echo "[RUSH-B.ORG] Installing MatchZy (with CounterStrikeSharp)..."
    TMPZIP="/tmp/matchzy.zip"
    curl -fsSL -o "${TMPZIP}" "${MATCHZY_URL}" 2>/dev/null
    if [ -f "${TMPZIP}" ]; then
        cd "${CSGO_DIR}" && unzip -o "${TMPZIP}" 2>/dev/null || echo "  MatchZy extract failed"
        rm -f "${TMPZIP}"
        echo "  MatchZy + CounterStrikeSharp installed"
    else
        echo "  MatchZy download failed"
    fi

    touch "${PLUGIN_MARKER}"
    echo "[RUSH-B.ORG] Plugins installed"
fi

# ─── Copy configs ─────────────────────────────────────────
echo "[RUSH-B.ORG] Applying configs..."

if [ -d /instance/config ] && [ -d "${CSGO_DIR}" ]; then
    mkdir -p "${CSGO_DIR}/cfg"
    cp -f /instance/config/*.cfg "${CSGO_DIR}/cfg/" 2>/dev/null || true
    echo "  server.cfg applied"
fi

if [ -f /shared/admins_simple.ini ] && [ -d "${CSGO_DIR}" ]; then
    mkdir -p "${CSGO_DIR}/addons/counterstrikesharp/configs"
    cp -f /shared/admins_simple.ini "${CSGO_DIR}/addons/counterstrikesharp/configs/admins_simple.ini" 2>/dev/null || true
fi

if [ -d /custom/plugins ] && [ -d "${CSGO_DIR}" ]; then
    mkdir -p "${CSGO_DIR}/addons/counterstrikesharp/plugins"
    cp -f /custom/plugins/*.dll "${CSGO_DIR}/addons/counterstrikesharp/plugins/" 2>/dev/null || true
    cp -f /custom/plugins/*.smx "${CSGO_DIR}/addons/counterstrikesharp/plugins/" 2>/dev/null || true
    echo "  custom plugins applied"
fi

if [ -d /custom/maps ] && [ -d "${CSGO_DIR}" ]; then
    mkdir -p "${CSGO_DIR}/maps"
    cp -f /custom/maps/*.bsp "${CSGO_DIR}/maps/" 2>/dev/null || true
    cp -f /custom/maps/*.nav "${CSGO_DIR}/maps/" 2>/dev/null || true
    echo "  custom maps applied"
fi

# ─── Start CS2 ───────────────────────────────────────────
echo "[RUSH-B.ORG] Starting CS2 server on port ${CS2_PORT:-27015}..."

export LD_LIBRARY_PATH="${CS2_DIR}/game/bin/linuxsteamrt64:${LD_LIBRARY_PATH}"

exec "${CS2_DIR}/game/bin/linuxsteamrt64/cs2" -dedicated \
    -port "${CS2_PORT:-27015}" \
    +tv_port "${CS2_GOTV_PORT:-27020}" \
    +map "${CS2_MAP:-de_mirage}" \
    +game_type 0 +game_mode 1 \
    +exec server.cfg \
    -usercon

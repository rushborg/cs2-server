#!/bin/bash
# RUSH-B.ORG CS2 Server Entrypoint
# CS2 base + plugins are pre-installed via agent's setupBase() into overlayfs.
# This entrypoint only applies per-instance configs and starts CS2.

CS2_DIR=/home/steam/cs2-dedicated
CSGO_DIR="${CS2_DIR}/game/csgo"

# ─── Verify CS2 is present ──────────────────────────────
if [ ! -f "${CS2_DIR}/game/bin/linuxsteamrt64/cs2" ]; then
    echo "[RUSH-B.ORG] ERROR: CS2 not found at ${CS2_DIR}"
    echo "  CS2 base must be installed via agent setup_base command"
    exit 1
fi

# ─── Fix steamclient.so (if SteamCMD version available) ─
if [ -f "/home/steam/steamcmd/linux64/steamclient.so" ] && [ -d "${CS2_DIR}/game/bin/linuxsteamrt64/" ]; then
    cp -f /home/steam/steamcmd/linux64/steamclient.so "${CS2_DIR}/game/bin/linuxsteamrt64/steamclient.so" 2>/dev/null || true
    echo "[RUSH-B.ORG] steamclient.so updated"
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

GSLT_ARG=""
if [ -n "${CS2_GSLT}" ]; then
    GSLT_ARG="+sv_setsteamaccount ${CS2_GSLT}"
    echo "[RUSH-B.ORG] GSLT token configured"
fi

export LD_LIBRARY_PATH="${CS2_DIR}/game/bin/linuxsteamrt64:${LD_LIBRARY_PATH}"

exec "${CS2_DIR}/game/bin/linuxsteamrt64/cs2" -dedicated \
    -port "${CS2_PORT:-27015}" \
    +tv_port "${CS2_GOTV_PORT:-27020}" \
    +map "${CS2_MAP:-de_mirage}" \
    +game_type 0 +game_mode 1 \
    +exec server.cfg \
    ${GSLT_ARG} \
    -usercon

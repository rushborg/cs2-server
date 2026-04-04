#!/bin/bash
# RUSH-B.ORG CS2 Server Entrypoint
# Each container has its own writable CS2 copy (hardlinked from cs2-base).
# No shared mutable state between containers.

set -e

log() { echo "[$(date '+%H:%M:%S')] [RUSH-B.ORG] $1"; }

CS2_DIR=/home/steam/cs2-dedicated
CSGO_DIR="${CS2_DIR}/game/csgo"
PLUGIN_MARKER="${CSGO_DIR}/addons/.rushborg-plugins-installed"

# Plugin URLs
METAMOD_URL="https://mms.alliedmods.net/mmsdrop/2.0/mmsource-2.0.0-git1390-linux.tar.gz"
CSSHARP_URL="https://github.com/roflmuffin/CounterStrikeSharp/releases/download/v1.0.364/counterstrikesharp-with-runtime-linux-1.0.364.zip"
MATCHZY_URL="https://github.com/shobhit-pathak/MatchZy/releases/download/0.8.15/MatchZy-0.8.15.zip"

# ─── Ensure correct ownership ────────────────────────────
# cs2-data bind mount may be owned by host user (rushborgsrv).
# SteamCMD runs as steam user — needs write access.
chown -R steam:steam "${CS2_DIR}" 2>/dev/null || true

# ─── Install CS2 if not present ─────────────────────────
if [ ! -f "${CS2_DIR}/game/bin/linuxsteamrt64/cs2" ]; then
    log "CS2 not installed, running SteamCMD..."
    # Retry up to 5 times (SteamCMD often needs multiple runs for large downloads)
    for attempt in 1 2 3 4 5; do
        log "SteamCMD attempt ${attempt}/5..."
        gosu steam /usr/games/steamcmd \
            +force_install_dir "${CS2_DIR}" \
            +login anonymous \
            +app_info_update 1 \
            +app_update 730 validate \
            +quit || true
        if [ -f "${CS2_DIR}/game/bin/linuxsteamrt64/cs2" ]; then
            log "CS2 installed successfully"
            break
        fi
        log "SteamCMD attempt ${attempt} incomplete, retrying in 10s..."
        sleep 10
    done
    if [ ! -f "${CS2_DIR}/game/bin/linuxsteamrt64/cs2" ]; then
        log "ERROR: CS2 installation failed after 5 attempts. Waiting 60s before container restart..."
        sleep 60
        exit 1
    fi
fi

# ─── Fix steamclient.so ─────────────────────────────────
# Fix steamclient.so — copy from SteamCMD installation
STEAMCLIENT_PATHS="/home/steam/.steam/steamcmd/linux64/steamclient.so /home/steam/.local/share/Steam/steamcmd/linux64/steamclient.so /usr/lib/games/linux64/steamclient.so"
for sc in $STEAMCLIENT_PATHS; do
    if [ -f "$sc" ] && [ -d "${CS2_DIR}/game/bin/linuxsteamrt64/" ]; then
        cp -f "$sc" "${CS2_DIR}/game/bin/linuxsteamrt64/steamclient.so" 2>/dev/null || true
        break
    fi
done

# ─── Install plugins (once) ─────────────────────────────
if [ -d "${CSGO_DIR}" ] && [ ! -f "${PLUGIN_MARKER}" ]; then
    log "Installing plugins..."

    curl -fsSL "${METAMOD_URL}" | tar xz -C "${CSGO_DIR}/" 2>/dev/null || log "MetaMod install failed"

    curl -fsSL -o /tmp/cssharp.zip "${CSSHARP_URL}" 2>/dev/null
    if [ -f /tmp/cssharp.zip ]; then
        cd "${CSGO_DIR}" && unzip -o /tmp/cssharp.zip 2>/dev/null || true
        rm -f /tmp/cssharp.zip
    fi

    curl -fsSL -o /tmp/matchzy.zip "${MATCHZY_URL}" 2>/dev/null
    if [ -f /tmp/matchzy.zip ]; then
        cd "${CSGO_DIR}" && unzip -o /tmp/matchzy.zip 2>/dev/null || true
        rm -f /tmp/matchzy.zip
    fi

    chmod -R 755 "${CSGO_DIR}/addons/" 2>/dev/null || true
    chown -R steam:steam "${CSGO_DIR}/addons/" 2>/dev/null || true

    # CSSharp core config
    if [ -f "${CSGO_DIR}/addons/counterstrikesharp/configs/core.example.json" ] && \
       [ ! -f "${CSGO_DIR}/addons/counterstrikesharp/configs/core.json" ]; then
        cp "${CSGO_DIR}/addons/counterstrikesharp/configs/core.example.json" \
           "${CSGO_DIR}/addons/counterstrikesharp/configs/core.json"
    fi

    # Register CSSharp in MetaMod
    if [ -d "${CSGO_DIR}/addons/counterstrikesharp/bin" ] && [ ! -f "${CSGO_DIR}/addons/metamod/counterstrikesharp.vdf" ]; then
        cat > "${CSGO_DIR}/addons/metamod/counterstrikesharp.vdf" << 'VDFEOF'
"Plugin"
{
	"file"	"addons/counterstrikesharp/bin/linuxsteamrt64/counterstrikesharp"
}
VDFEOF
    fi

    touch "${PLUGIN_MARKER}"
    log "Plugins installed"
fi

# ─── Patch gameinfo.gi for MetaMod ──────────────────────
GAMEINFO="${CSGO_DIR}/gameinfo.gi"
if [ -f "${GAMEINFO}" ] && [ -d "${CSGO_DIR}/addons/metamod" ]; then
    if ! grep -q "metamod" "${GAMEINFO}"; then
        log "Patching gameinfo.gi for MetaMod..."
        sed -i '/Game_LowViolence/a\\t\t\tGame\tcsgo/addons/metamod' "${GAMEINFO}"
    fi
fi

# ─── CSSharp log file ───────────────────────────────────
if [ -d "${CSGO_DIR}/addons/counterstrikesharp" ]; then
    touch "${CSGO_DIR}/addons/counterstrikesharp/counterstrikesharp.log" 2>/dev/null || true
    chmod 666 "${CSGO_DIR}/addons/counterstrikesharp/counterstrikesharp.log" 2>/dev/null || true
fi

# ─── Apply configs ──────────────────────────────────────
log "Applying configs..."

if [ -d /instance/config ] && [ -d "${CSGO_DIR}" ]; then
    mkdir -p "${CSGO_DIR}/cfg"
    cp -f /instance/config/*.cfg "${CSGO_DIR}/cfg/" 2>/dev/null || true
fi

if [ -f /shared/admins_simple.ini ] && [ -d "${CSGO_DIR}" ]; then
    mkdir -p "${CSGO_DIR}/addons/counterstrikesharp/configs"
    cp -f /shared/admins_simple.ini "${CSGO_DIR}/addons/counterstrikesharp/configs/admins_simple.ini" 2>/dev/null || true
fi

if [ -d /custom/plugins ] && [ -d "${CSGO_DIR}" ]; then
    mkdir -p "${CSGO_DIR}/addons/counterstrikesharp/plugins"
    cp -f /custom/plugins/*.dll "${CSGO_DIR}/addons/counterstrikesharp/plugins/" 2>/dev/null || true
fi

if [ -d /custom/maps ] && [ -d "${CSGO_DIR}" ]; then
    mkdir -p "${CSGO_DIR}/maps"
    cp -f /custom/maps/*.bsp "${CSGO_DIR}/maps/" 2>/dev/null || true
    cp -f /custom/maps/*.nav "${CSGO_DIR}/maps/" 2>/dev/null || true
fi

# ─── Override MatchZy chat prefix ───────────────────────
MATCHZY_CFG="${CSGO_DIR}/cfg/MatchZy/config.cfg"
if [ -f "${MATCHZY_CFG}" ] && ! grep -q "RUSH-B.ORG" "${MATCHZY_CFG}" 2>/dev/null; then
    sed -i 's|^matchzy_chat_prefix.*|matchzy_chat_prefix [{Green}RUSH-B.ORG{Default}]|' "${MATCHZY_CFG}"
    log "MatchZy chat prefix set to [RUSH-B.ORG]"
fi

# ─── Start CS2 ──────────────────────────────────────────
log "Starting CS2 on port ${CS2_PORT:-27015}..."

GSLT_ARG=""
if [ -n "${CS2_GSLT}" ]; then
    GSLT_ARG="+sv_setsteamaccount ${CS2_GSLT}"
    log "GSLT token configured"
fi

export LD_LIBRARY_PATH="${CS2_DIR}/game/bin/linuxsteamrt64:${LD_LIBRARY_PATH}"

chown -R steam:steam /instance/data 2>/dev/null || true
chown -R steam:steam /demos 2>/dev/null || true

exec gosu steam "${CS2_DIR}/game/bin/linuxsteamrt64/cs2" -dedicated \
    +ip 0.0.0.0 \
    -port "${CS2_PORT:-27015}" \
    +tv_port "${CS2_GOTV_PORT:-27020}" \
    -maxplayers "${CS2_MAXPLAYERS:-10}" \
    +map "${CS2_MAP:-de_mirage}" \
    +game_type "${CS2_GAME_TYPE:-0}" +game_mode "${CS2_GAME_MODE:-1}" \
    +exec server.cfg \
    ${GSLT_ARG} \
    -usercon

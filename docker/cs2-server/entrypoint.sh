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

# ─── Verify CS2 is present ───────────────────────────────
# CS2 is pre-installed by the agent into cs2-data via SteamCMD on the host.
# Container only handles plugins, configs, and running CS2.
if [ ! -f "${CS2_DIR}/game/bin/linuxsteamrt64/cs2" ]; then
    log "ERROR: CS2 not found. Agent must install CS2 into cs2-data before starting container."
    log "Waiting 60s before restart..."
    sleep 60
    exit 1
fi

# ─── Sync steam uid to bind-mount owner ─────────────────
# The host agent installs CS2 into cs2-base as its own user (e.g. rushborgsrv)
# and then hardlinks cs2-base into each instance's cs2-data via `cp -al`.
# If we `chown -R steam:steam` the bind-mounted cs2-dedicated, the chown walks
# through the hardlinked inodes and ALSO changes ownership of cs2-base files,
# which then makes the next `cp -al` fail with EPERM under
# fs.protected_hardlinks=1 (non-root users cannot hardlink files they don't
# own). Instead, we align the in-container `steam` uid/gid with whatever user
# already owns the bind mount, so no chown is necessary and the hardlinks stay
# consistent with cs2-base.
TARGET_UID=$(stat -c %u "${CS2_DIR}" 2>/dev/null || echo 1000)
TARGET_GID=$(stat -c %g "${CS2_DIR}" 2>/dev/null || echo 1000)
CURRENT_STEAM_UID=$(id -u steam 2>/dev/null || echo 1000)
CURRENT_STEAM_GID=$(id -g steam 2>/dev/null || echo 1000)
if [ "${TARGET_UID}" != "0" ] && [ "${TARGET_UID}" != "${CURRENT_STEAM_UID}" ]; then
    log "Re-mapping steam uid ${CURRENT_STEAM_UID} -> ${TARGET_UID} to match host bind mount owner"
    groupmod -o -g "${TARGET_GID}" steam 2>/dev/null || true
    usermod  -o -u "${TARGET_UID}" -g "${TARGET_GID}" steam 2>/dev/null || true
fi

# ─── Steamworks SDK shim ────────────────────────────────
# CS2 gameserver инициализирует Steamworks SDK и ищет steamclient.so в
# /home/steam/.steam/sdk{32,64}/. Без совместимой версии сервер запускается,
# даже логинится с GSLT, но SteamNetworkingSockets падают с:
#   [S_API FAIL] Tried to access Steam interface SteamUtils010 before ...
# из-за чего клиентские коннекты молча отбрасываются (A2S при этом отвечает).
#
# Источник правды — steamclient.so, который host'овый steamcmd кладёт рядом с
# установкой CS2 через stageSteamclientSO() в agent/internal/commands. Он
# копируется как РЕАЛЬНЫЙ файл в <CS2_DIR>/steamclient/linux{64,32}/ и ровно
# соответствует версии Source 2 runtime, поставленной в том же проходе
# steamcmd. Раньше тут был fallback, который качал свежий steamcmd с CDN и
# брал оттуда steamclient.so — это приводило к несовместимости API и тихо
# ломало клиентские коннекты. Fallback удалён намеренно.
setup_steamclient_shim() {
    local sdk64=/home/steam/.steam/sdk64
    local sdk32=/home/steam/.steam/sdk32
    mkdir -p "$sdk64" "$sdk32"

    local src64="" src32=""
    for p in \
        "${CS2_DIR}/steamclient/linux64/steamclient.so" \
        "${CS2_DIR}/linux64/steamclient.so" \
        "${CS2_DIR}/game/bin/linuxsteamrt64/steamclient.so"; do
        if [ -f "$p" ]; then src64="$p"; break; fi
    done
    for p in \
        "${CS2_DIR}/steamclient/linux32/steamclient.so" \
        "${CS2_DIR}/linux32/steamclient.so" \
        "${CS2_DIR}/game/bin/linuxsteamrt32/steamclient.so"; do
        if [ -f "$p" ]; then src32="$p"; break; fi
    done

    if [ -n "$src64" ]; then
        ln -sf "$src64" "$sdk64/steamclient.so"
        log "Linked sdk64/steamclient.so -> $src64"
    else
        log "FATAL: steamclient.so (64) not found under ${CS2_DIR}/steamclient/linux64/."
        log "       The agent must stage it via stageSteamclientSO() after steamcmd install."
        log "       Re-deploy the server from the admin UI so the agent copies it in."
    fi
    if [ -n "$src32" ]; then
        ln -sf "$src32" "$sdk32/steamclient.so"
    fi

    chown -R steam:steam /home/steam/.steam 2>/dev/null || true
}
setup_steamclient_shim

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
    # NOTE: no chown here — addons/ sits inside the hardlinked cs2-dedicated
    # tree. A recursive chown would leak into cs2-base inodes and break
    # subsequent `cp -al` from the agent. Plugin files are fresh (created
    # inside the container) and already belong to the bind-mount owner uid
    # via our entrypoint usermod.

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
    cp --remove-destination /instance/config/*.cfg "${CSGO_DIR}/cfg/" 2>/dev/null || true
fi

if [ -f /shared/admins_simple.ini ] && [ -d "${CSGO_DIR}" ]; then
    mkdir -p "${CSGO_DIR}/addons/counterstrikesharp/configs"
    cp --remove-destination /shared/admins_simple.ini "${CSGO_DIR}/addons/counterstrikesharp/configs/admins_simple.ini" 2>/dev/null || true
fi

if [ -d /custom/plugins ] && [ -d "${CSGO_DIR}" ]; then
    mkdir -p "${CSGO_DIR}/addons/counterstrikesharp/plugins"
    cp --remove-destination /custom/plugins/*.dll "${CSGO_DIR}/addons/counterstrikesharp/plugins/" 2>/dev/null || true
fi

if [ -d /custom/maps ] && [ -d "${CSGO_DIR}" ]; then
    mkdir -p "${CSGO_DIR}/maps"
    cp --remove-destination /custom/maps/*.bsp "${CSGO_DIR}/maps/" 2>/dev/null || true
    cp --remove-destination /custom/maps/*.nav "${CSGO_DIR}/maps/" 2>/dev/null || true
fi

# ─── Override MatchZy config.cfg ─────────────────────────
# MatchZy reads its ConVars ONLY from cfg/MatchZy/config.cfg at plugin init.
# Our cfg/matchzy.cfg (exec'd from server.cfg) sets generic server-level
# settings but MatchZy-specific ConVars must live in its own config file.
MATCHZY_CFG="${CSGO_DIR}/cfg/MatchZy/config.cfg"
if [ -f "${MATCHZY_CFG}" ]; then
    # Chat prefix
    if ! grep -q "RUSH-B.ORG" "${MATCHZY_CFG}" 2>/dev/null; then
        sed -i 's|^matchzy_chat_prefix.*|matchzy_chat_prefix [{Green}RUSH-B.ORG{Default}]|' "${MATCHZY_CFG}"
        log "MatchZy chat prefix set to [RUSH-B.ORG]"
    fi

    # Whitelist enforcement — idle server without a loaded match kicks everyone;
    # when a match is loaded, whitelist is populated from team1/team2/spectators.
    if ! grep -q "matchzy_kick_when_no_match_loaded" "${MATCHZY_CFG}" 2>/dev/null; then
        printf '\n// RUSH-B.ORG: whitelist enforcement\nmatchzy_kick_when_no_match_loaded true\nmatchzy_whitelist_enabled_default true\n' >> "${MATCHZY_CFG}"
        log "MatchZy whitelist enforcement enabled"
    else
        sed -i 's|^matchzy_kick_when_no_match_loaded.*|matchzy_kick_when_no_match_loaded true|' "${MATCHZY_CFG}"
        sed -i 's|^matchzy_whitelist_enabled_default.*|matchzy_whitelist_enabled_default true|' "${MATCHZY_CFG}"
    fi
fi

# ─── Start CS2 ──────────────────────────────────────────
log "Starting CS2 on port ${CS2_PORT:-27015}..."

GSLT_ARG=""
if [ -n "${CS2_GSLT}" ]; then
    GSLT_ARG="+sv_setsteamaccount ${CS2_GSLT}"
    log "GSLT token configured"
fi

# CS2 no longer loads rcon_password from +exec server.cfg — it must be
# passed directly on the command line.
RCON_ARG=""
if [ -n "${CS2_RCON_PASSWORD}" ]; then
    RCON_ARG="+rcon_password ${CS2_RCON_PASSWORD}"
fi

export LD_LIBRARY_PATH="${CS2_DIR}/game/bin/linuxsteamrt64:${LD_LIBRARY_PATH}"

chown -R steam:steam /instance/data 2>/dev/null || true
chown -R steam:steam /demos 2>/dev/null || true

CS2_TICKRATE="${CS2_TICKRATE:-128}"
log "Tickrate: ${CS2_TICKRATE}"

# ─── Steam socket port ──────────────────────────────────
# CS2 uses a separate UDP socket for Steam master registration / client auth
# handshake. Without an explicit -steamport, multiple instances running under
# network_mode: host collide on the auto-assigned port: A2S (on main -port)
# still responds on all instances, but client `connect` and Steam server
# browser registration only work for whichever instance grabbed the socket
# first. Derive a unique steamport from the game port using the classic
# Source-engine convention (game_port - 10) unless explicitly overridden.
CS2_GAME_PORT="${CS2_PORT:-27015}"
if [ -z "${CS2_STEAM_PORT}" ]; then
    CS2_STEAM_PORT=$((CS2_GAME_PORT - 10))
fi
log "Steam port: ${CS2_STEAM_PORT}"

exec gosu steam "${CS2_DIR}/game/bin/linuxsteamrt64/cs2" -dedicated \
    +ip 0.0.0.0 \
    -port "${CS2_GAME_PORT}" \
    -steamport "${CS2_STEAM_PORT}" \
    +tv_port "${CS2_GOTV_PORT:-27020}" \
    -maxplayers "${CS2_MAXPLAYERS:-10}" \
    -tickrate "${CS2_TICKRATE}" \
    +map "${CS2_MAP:-de_mirage}" \
    +game_type "${CS2_GAME_TYPE:-0}" +game_mode "${CS2_GAME_MODE:-1}" \
    +exec server.cfg \
    ${RCON_ARG} \
    ${GSLT_ARG} \
    -usercon

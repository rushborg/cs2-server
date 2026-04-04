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

# Fix ownership
chown -R steam:steam "${CS2_DIR}" 2>/dev/null || true

# ─── Steamworks SDK shim ────────────────────────────────
# CS2 gameserver инициализирует Steamworks SDK и ищет steamclient.so в
# /home/steam/.steam/sdk{32,64}/. Без этого сервер падает с:
#   "Failed to load module '/home/steam/.steam/sdk64/steamclient.so'"
# steamcmd при установке app 730 кладёт свою копию в <installdir>/linux64/,
# поэтому делаем симлинки оттуда. Если файла нет (например, база приехала
# без linux64/ директории) — подкачаем steamcmd и возьмём из него.
setup_steamclient_shim() {
    local sdk64=/home/steam/.steam/sdk64
    local sdk32=/home/steam/.steam/sdk32
    mkdir -p "$sdk64" "$sdk32"

    local src64="" src32=""
    for p in \
        "${CS2_DIR}/linux64/steamclient.so" \
        "${CS2_DIR}/game/bin/linuxsteamrt64/steamclient.so" \
        "/usr/lib/steamcmd/linux64/steamclient.so"; do
        if [ -f "$p" ]; then src64="$p"; break; fi
    done
    for p in \
        "${CS2_DIR}/linux32/steamclient.so" \
        "${CS2_DIR}/game/bin/linuxsteamrt32/steamclient.so" \
        "/usr/lib/steamcmd/linux32/steamclient.so"; do
        if [ -f "$p" ]; then src32="$p"; break; fi
    done

    if [ -z "$src64" ]; then
        log "steamclient.so not found in CS2 install — fetching from Valve CDN"
        mkdir -p /tmp/scshim
        if curl -fsSL https://media.steampowered.com/client/steamcmd_linux.tar.gz -o /tmp/scshim/steamcmd.tgz; then
            tar xzf /tmp/scshim/steamcmd.tgz -C /tmp/scshim 2>/dev/null || true
            # steamcmd скачивает steamclient.so только после первого запуска
            (cd /tmp/scshim && HOME=/tmp/scshim ./steamcmd.sh +login anonymous +quit 2>/dev/null || true)
            for p in \
                /tmp/scshim/linux64/steamclient.so \
                /tmp/scshim/.steam/steamcmd/linux64/steamclient.so \
                /tmp/scshim/.local/share/Steam/linux64/steamclient.so; do
                if [ -f "$p" ]; then src64="$p"; break; fi
            done
            for p in \
                /tmp/scshim/linux32/steamclient.so \
                /tmp/scshim/.steam/steamcmd/linux32/steamclient.so \
                /tmp/scshim/.local/share/Steam/linux32/steamclient.so; do
                if [ -f "$p" ]; then src32="$p"; break; fi
            done
        fi
    fi

    if [ -n "$src64" ]; then
        ln -sf "$src64" "$sdk64/steamclient.so"
        log "Linked sdk64/steamclient.so -> $src64"
    else
        log "WARN: steamclient.so (64) not found — CS2 может не запуститься"
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

CS2_TICKRATE="${CS2_TICKRATE:-128}"
log "Tickrate: ${CS2_TICKRATE}"

exec gosu steam "${CS2_DIR}/game/bin/linuxsteamrt64/cs2" -dedicated \
    +ip 0.0.0.0 \
    -port "${CS2_PORT:-27015}" \
    +tv_port "${CS2_GOTV_PORT:-27020}" \
    -maxplayers "${CS2_MAXPLAYERS:-10}" \
    -tickrate "${CS2_TICKRATE}" \
    +map "${CS2_MAP:-de_mirage}" \
    +game_type "${CS2_GAME_TYPE:-0}" +game_mode "${CS2_GAME_MODE:-1}" \
    +exec server.cfg \
    ${GSLT_ARG} \
    -usercon

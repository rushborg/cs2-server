#!/bin/bash
# RUSH-B.ORG CS2 Server Entrypoint
# CS2 base is shared across instances via bind mount.
# Per-instance writable dirs are symlinked from /instance/data/.

set -e

CS2_DIR=/home/steam/cs2-dedicated
CSGO_DIR="${CS2_DIR}/game/csgo"
INSTANCE_DATA="/instance/data"

# Plugin URLs — used by setup if base is empty
METAMOD_URL="https://mms.alliedmods.net/mmsdrop/2.0/mmsource-2.0.0-git1390-linux.tar.gz"
CSSHARP_URL="https://github.com/roflmuffin/CounterStrikeSharp/releases/download/v1.0.364/counterstrikesharp-with-runtime-linux-1.0.364.zip"
MATCHZY_URL="https://github.com/shobhit-pathak/MatchZy/releases/download/0.8.15/MatchZy-0.8.15.zip"
PLUGIN_MARKER="${CSGO_DIR}/addons/.rushborg-plugins-installed"

# ─── Install CS2 if not present (first container on this host) ───
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

# ─── Fix steamclient.so ─────────────────────────────────
if [ -f "/home/steam/steamcmd/linux64/steamclient.so" ] && [ -d "${CS2_DIR}/game/bin/linuxsteamrt64/" ]; then
    cp -f /home/steam/steamcmd/linux64/steamclient.so "${CS2_DIR}/game/bin/linuxsteamrt64/steamclient.so" 2>/dev/null || true
fi

# ─── Install plugins (once per base) ────────────────────
if [ -d "${CSGO_DIR}" ] && [ ! -f "${PLUGIN_MARKER}" ]; then
    echo "[RUSH-B.ORG] Installing plugins..."

    echo "  Installing MetaMod..."
    curl -fsSL "${METAMOD_URL}" | tar xz -C "${CSGO_DIR}/" 2>/dev/null || echo "  MetaMod install failed"

    echo "  Installing CounterStrikeSharp..."
    curl -fsSL -o /tmp/cssharp.zip "${CSSHARP_URL}" 2>/dev/null
    if [ -f /tmp/cssharp.zip ]; then
        cd "${CSGO_DIR}" && unzip -o /tmp/cssharp.zip 2>/dev/null || echo "  CSSharp extract failed"
        rm -f /tmp/cssharp.zip
    fi

    echo "  Installing MatchZy..."
    curl -fsSL -o /tmp/matchzy.zip "${MATCHZY_URL}" 2>/dev/null
    if [ -f /tmp/matchzy.zip ]; then
        cd "${CSGO_DIR}" && unzip -o /tmp/matchzy.zip 2>/dev/null || echo "  MatchZy extract failed"
        rm -f /tmp/matchzy.zip
    fi

    # Fix permissions
    chmod -R 777 "${CSGO_DIR}/addons/" 2>/dev/null || true

    # Create core.json for CSSharp
    if [ -f "${CSGO_DIR}/addons/counterstrikesharp/configs/core.example.json" ] && \
       [ ! -f "${CSGO_DIR}/addons/counterstrikesharp/configs/core.json" ]; then
        cp "${CSGO_DIR}/addons/counterstrikesharp/configs/core.example.json" \
           "${CSGO_DIR}/addons/counterstrikesharp/configs/core.json"
    fi

    # Register CounterStrikeSharp in MetaMod
    if [ -d "${CSGO_DIR}/addons/counterstrikesharp/bin" ] && [ ! -f "${CSGO_DIR}/addons/metamod/counterstrikesharp.vdf" ]; then
        cat > "${CSGO_DIR}/addons/metamod/counterstrikesharp.vdf" << 'VDFEOF'
"Plugin"
{
	"file"	"addons/counterstrikesharp/bin/linuxsteamrt64/counterstrikesharp"
}
VDFEOF
    fi

    touch "${PLUGIN_MARKER}"
    echo "[RUSH-B.ORG] Plugins installed"
fi

# ─── Patch gameinfo.gi for MetaMod (idempotent) ─────────
GAMEINFO="${CSGO_DIR}/gameinfo.gi"
if [ -f "${GAMEINFO}" ] && [ -d "${CSGO_DIR}/addons/metamod" ]; then
    if ! grep -q "metamod" "${GAMEINFO}"; then
        echo "[RUSH-B.ORG] Patching gameinfo.gi for MetaMod..."
        sed -i '/Game_LowViolence/a\\t\t\tGame\tcsgo/addons/metamod' "${GAMEINFO}"
    fi
fi

# ─── Per-instance writable directories ──────────────────
# CS2 and plugins write to certain directories. We redirect these
# to per-instance storage so multiple servers can share the base.
mkdir -p "${INSTANCE_DATA}/cssharp-data" \
         "${INSTANCE_DATA}/matchzy-data" \
         "${INSTANCE_DATA}/logs"

# CSSharp logs + plugin configs (writable)
CSSHARP_DIR="${CSGO_DIR}/addons/counterstrikesharp"
if [ -d "${CSSHARP_DIR}" ]; then
    # Redirect logs dir
    rm -rf "${CSSHARP_DIR}/logs" 2>/dev/null || true
    ln -sfn "${INSTANCE_DATA}/cssharp-data/logs" "${CSSHARP_DIR}/logs"
    mkdir -p "${INSTANCE_DATA}/cssharp-data/logs"

    # Redirect plugin configs (MatchZy writes here)
    if [ ! -d "${INSTANCE_DATA}/cssharp-data/plugins-cfg" ]; then
        mkdir -p "${INSTANCE_DATA}/cssharp-data/plugins-cfg"
        # Copy existing plugin configs on first run
        cp -r "${CSSHARP_DIR}/configs/plugins/"* "${INSTANCE_DATA}/cssharp-data/plugins-cfg/" 2>/dev/null || true
    fi
    rm -rf "${CSSHARP_DIR}/configs/plugins" 2>/dev/null || true
    ln -sfn "${INSTANCE_DATA}/cssharp-data/plugins-cfg" "${CSSHARP_DIR}/configs/plugins"

    # CSSharp writes counterstrikesharp.log next to its binary
    touch "${CSSHARP_DIR}/counterstrikesharp.log" 2>/dev/null || true
    chmod 666 "${CSSHARP_DIR}/counterstrikesharp.log" 2>/dev/null || true
fi

# CS2 server logs
if [ -d "${CSGO_DIR}" ]; then
    rm -rf "${CSGO_DIR}/logs" 2>/dev/null || true
    ln -sfn "${INSTANCE_DATA}/logs" "${CSGO_DIR}/logs"
fi

# ─── Copy configs ─────────────────────────────────────────
echo "[RUSH-B.ORG] Applying configs..."

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

# ─── Start CS2 ───────────────────────────────────────────
echo "[RUSH-B.ORG] Starting CS2 on port ${CS2_PORT:-27015}..."

GSLT_ARG=""
if [ -n "${CS2_GSLT}" ]; then
    GSLT_ARG="+sv_setsteamaccount ${CS2_GSLT}"
    echo "[RUSH-B.ORG] GSLT token configured"
fi

export LD_LIBRARY_PATH="${CS2_DIR}/game/bin/linuxsteamrt64:${LD_LIBRARY_PATH}"

# Fix ownership of writable dirs
chown -R steam:steam "${INSTANCE_DATA}" 2>/dev/null || true
chown -R steam:steam "${CSGO_DIR}/addons/" 2>/dev/null || true

# Drop to steam user for CS2 process (security: CS2 never runs as root)
exec gosu steam "${CS2_DIR}/game/bin/linuxsteamrt64/cs2" -dedicated \
    -port "${CS2_PORT:-27015}" \
    +tv_port "${CS2_GOTV_PORT:-27020}" \
    +map "${CS2_MAP:-de_mirage}" \
    +game_type 0 +game_mode 1 \
    +exec server.cfg \
    ${GSLT_ARG} \
    -usercon

#!/usr/bin/env bash
#
# bootstrap-host-perf.sh — host-level performance tuning for CS2 game servers.
#
# Лечит "UNEXPECTED LONG FRAME DETECTED" — длинные паузы тика на сервере, которые
# у клиентов выглядят как лаг. Корни — в трёх стандартных проблемах Linux под
# реальные нагрузки:
#
#   1. CPU governor "powersave" / "ondemand" — частота скачет, тик 64Hz попадает
#      в момент когда ядро работает на 800MHz, фрейм затягивается на 100+ ms.
#   2. Transparent Hugepages (THP) defrag — ядро решает скомпактить память
#      именно во время игры, freeze всех процессов на десятки ms.
#   3. swap включён — если хост под нагрузкой, страницы CS2 могут уйти на диск.
#
# Скрипт идемпотентный: безопасно запускать повторно. Требует root.
#
# Использование:
#   sudo bash bootstrap-host-perf.sh
#
# После применения нужно один раз ребутнуть хост чтобы governor встал
# до старта docker (иначе контейнеры стартанут на старом профиле).

set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then
  echo "[bootstrap-perf] must run as root" >&2
  exit 1
fi

echo "[bootstrap-perf] === RushBorg CS2 host performance tuning ==="

# ─── 1. CPU governor → performance ─────────────────────────────────────
echo "[bootstrap-perf] (1/4) installing cpufrequtils + setting governor=performance"
if ! command -v cpufreq-info >/dev/null 2>&1; then
  apt-get update -qq
  DEBIAN_FRONTEND=noninteractive apt-get install -y -qq cpufrequtils
fi
echo 'GOVERNOR="performance"' > /etc/default/cpufrequtils
systemctl restart cpufrequtils 2>/dev/null || true
systemctl enable cpufrequtils 2>/dev/null || true
# На случай если systemd не подхватил — выставим вручную всем CPU.
for cpu in /sys/devices/system/cpu/cpu*/cpufreq/scaling_governor; do
  [ -w "$cpu" ] && echo performance > "$cpu" || true
done
echo "[bootstrap-perf]   governor: $(cat /sys/devices/system/cpu/cpu0/cpufreq/scaling_governor 2>/dev/null || echo unknown)"

# ─── 2. Transparent Hugepages: disable + disable defrag ────────────────
echo "[bootstrap-perf] (2/4) disabling transparent hugepages"
cat > /etc/systemd/system/disable-thp.service <<'EOF'
[Unit]
Description=Disable Transparent Hugepages (CS2 long-frame fix)
After=local-fs.target
DefaultDependencies=no

[Service]
Type=oneshot
ExecStart=/bin/sh -c 'echo never > /sys/kernel/mm/transparent_hugepage/enabled && echo never > /sys/kernel/mm/transparent_hugepage/defrag'
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable --now disable-thp.service
echo "[bootstrap-perf]   thp enabled: $(cat /sys/kernel/mm/transparent_hugepage/enabled)"
echo "[bootstrap-perf]   thp defrag : $(cat /sys/kernel/mm/transparent_hugepage/defrag)"

# ─── 3. swappiness → 0, dirty ratio → low ──────────────────────────────
echo "[bootstrap-perf] (3/4) tuning vm.swappiness + dirty ratios"
cat > /etc/sysctl.d/90-rushborg-cs2.conf <<'EOF'
# RushBorg CS2 — minimize swap pressure & write stalls during matches.
vm.swappiness = 1
vm.dirty_ratio = 10
vm.dirty_background_ratio = 5
# net.core defaults are usually fine; tweak only if хост под heavy traffic.
EOF
sysctl --system >/dev/null
echo "[bootstrap-perf]   swappiness: $(cat /proc/sys/vm/swappiness)"

# ─── 4. sudoers entry for agent realtime + ufw ─────────────────────────
# Не трогаем sudoers если он уже настроен installer'ом (там полный
# набор entries — cp/chmod/systemctl restart для self-update + ufw +
# chrt + ссылка на сам этот скрипт). Затирание сейчас удалит остальные
# entries и сломает self-update agent'а. Создаём только если файла нет
# (например когда оператор запустил скрипт руками без installer'а).
echo "[bootstrap-perf] (4/4) configuring sudoers for rushborg-agent"
SUDOERS_FILE=/etc/sudoers.d/rushborg-agent
if [ -f "${SUDOERS_FILE}" ] && grep -q "NOPASSWD" "${SUDOERS_FILE}"; then
  echo "[bootstrap-perf]   sudoers already configured by installer — skip"
else
  SUDO_USER_NAME="${SUDO_USER:-rushborgsrv}"
  if ! id rushborgsrv >/dev/null 2>&1; then
    if id rushborg >/dev/null 2>&1; then
      SUDO_USER_NAME="rushborg"
    fi
  fi
  CHRT_PATH="$(command -v chrt || echo /usr/bin/chrt)"
  UFW_PATH="$(command -v ufw || echo /usr/sbin/ufw)"
  cat > "${SUDOERS_FILE}" <<EOF
# Allow rushborg-agent to bump CS2 process to realtime priority and
# manage UFW rules without an interactive password prompt.
${SUDO_USER_NAME} ALL=(root) NOPASSWD: ${CHRT_PATH}, ${UFW_PATH}
EOF
  chmod 0440 "${SUDOERS_FILE}"
  visudo -c -f "${SUDOERS_FILE}" >/dev/null
  echo "[bootstrap-perf]   sudoers user: ${SUDO_USER_NAME}"
  echo "[bootstrap-perf]   chrt path   : ${CHRT_PATH}"
fi

echo ""
echo "[bootstrap-perf] === DONE ==="
echo "[bootstrap-perf] Recommended: reboot host once so governor + THP settle"
echo "[bootstrap-perf] before CS2 containers start. After that the agent will"
echo "[bootstrap-perf] auto-bump cs2 process to SCHED_FIFO 80 on each deploy."

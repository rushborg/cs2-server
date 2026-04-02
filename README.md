# RUSH-B.ORG CS2 Server

Docker-образ CS2 выделенного сервера с предустановленными MetaMod, SourceMod и get5 для турнирной платформы [RUSH-B.ORG](https://rush-b.org).

## Компоненты

### CS2 Docker Image (`docker/cs2-server/`)

Образ на базе [cm2network/steamcmd](https://hub.docker.com/r/cm2network/steamcmd) с:
- CS2 Dedicated Server (app 730)
- MetaMod:Source 2.0
- SourceMod 1.12
- get5 v0.15.0

**Volumes:**
- `/instance/config` — конфиги инстанса (server.cfg, get5.cfg)
- `/shared` — общие файлы (admins_simple.ini)
- `/custom/plugins` — кастомные SourceMod плагины (.smx)
- `/custom/maps` — кастомные карты (.bsp, .nav)
- `/demos` — записи матчей

**Переменные окружения:**
- `CS2_PORT` — игровой порт (default: 27015)
- `CS2_GOTV_PORT` — GOTV порт (default: 27020)
- `CS2_MAP` — стартовая карта (default: de_mirage)

### Agent (`agent/`)

Go-агент для управления CS2 серверами на удалённых хостах. Подключается к платформе через WebSocket, получает команды на развёртывание/остановку/обновление серверов.

**Возможности:**
- Развёртывание CS2 серверов в Docker контейнерах
- Остановка и удаление серверов
- Обновление Docker образа
- Установка кастомных плагинов и карт
- Мониторинг ресурсов (CPU, RAM, Disk)
- Синхронизация admins_simple.ini
- Auto-reconnect при потере соединения

## Быстрый старт

### Сборка Docker образа

```bash
cd docker/cs2-server
docker build -t ghcr.io/rushborg/cs2-server:latest .
```

### Запуск сервера

```bash
docker run -d --network host \
  -e CS2_PORT=27015 \
  -e CS2_GOTV_PORT=27020 \
  -v ./config:/instance/config:ro \
  ghcr.io/rushborg/cs2-server:latest
```

### Сборка агента

```bash
cd agent
go build -o rushborg-agent ./cmd/rushborg-agent
```

## Лицензия

MIT

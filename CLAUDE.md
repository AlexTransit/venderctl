# CLAUDE.md — Руководство по проекту venderctl

## О проекте

**venderctl** — серверный backend для управления сетью торговых автоматов.
Multi-command CLI приложение на Go. MQTT — основной транспорт, PostgreSQL — хранилище.

---

## Структура папок

```
venderctl-1/
├── cmd/
│   ├── internal/cli/          # CLI-фреймворк (регистрация команд)
│   └── venderctl/
│       ├── main.go            # Точка входа, регистрация всех команд
│       ├── control/           # Отправка команд на автоматы (ping, exec, qr, lock)
│       ├── web/               # Web-сервер (Gin, порт 8085), REST + WebSocket
│       ├── passwd/            # Управление паролями MQTT-клиентов
│       ├── sponge/            # Сбор телеметрии из MQTT → PostgreSQL
│       ├── tax/               # Налоговые задачи (РФ 2019, Tinkoff QR)
│       └── telegram/          # Telegram-бот для управления
├── internal/
│   ├── state/                 # Глобальное состояние: конфиг, БД, список машин
│   └── tele/                  # MQTT-клиент, парсинг топиков, Protobuf
├── sql/
│   ├── schema.sql             # Схема БД
│   └── db.sql                 # Дополнительные объекты БД
├── script/
│   ├── build                  # Сборка бинарника
│   ├── test                   # Запуск тестов
│   ├── sql-fmt                # Форматирование SQL
│   └── remote-venderctl       # Управление удалённым экземпляром
├── venderctl.hcl              # Главный конфиг (HCL-формат)
└── .golangci.yml              # Конфигурация линтера
```

---

## Команды приложения

| Команда    | Файл                            | Назначение                                          |
|------------|---------------------------------|-----------------------------------------------------|
| `sponge`   | cmd/venderctl/sponge/           | Слушает MQTT, сохраняет телеметрию в БД             |
| `web`      | cmd/venderctl/web/              | REST API + WebSocket интерфейс                      |
| `telegram` | cmd/venderctl/telegram/         | Telegram-бот для клиентов и администраторов         |
| `control`  | cmd/venderctl/control/          | CLI-управление машинами (ping, report, exec, lock)  |
| `tax`      | cmd/venderctl/tax/              | Обработка налоговых заданий (ФНС РФ 2019)           |
| `passwd`   | cmd/venderctl/passwd/           | Управление секретами/паролями MQTT                  |
| `version`  | cmd/venderctl/main.go           | Вывод версии                                        |

---

## Базы данных

**СУБД:** PostgreSQL 10+
**ORM:** `github.com/go-pg/pg/v9`
**Расширения:** `hstore` (ключ-значение, используется для инвентаря)

### Основные таблицы

| Таблица     | Назначение                                               |
|-------------|----------------------------------------------------------|
| `catalog`   | Каталог напитков (vmid, code, name)                      |
| `state`     | Текущее состояние каждой машины                          |
| `inventory` | Запасы товаров и кассет (hstore-поля)                    |
| `error`     | Лог ошибок машин                                         |
| `trans`     | История завершённых заказов (menu_code, price, method)   |
| `tax_job`   | Очередь налоговых заданий (ENUM: sched/busy/final/help)  |
| `ingest`    | Сырые пакеты телеметрии                                  |
| `tg_user`   | Пользователи Telegram (ban, balance, credit, diskont)    |

### Ключевые SQL-функции

```sql
state_update(vmid, state)       -- обновить состояние машины
connect_update(vmid, connect)   -- обновить статус подключения
tax_job_take(processor)         -- взять задание воркеру налогов
```

Подключение конфигурируется в `venderctl.hcl`:
```hcl
db {
  url = "postgresql://user:password@server/database"
}
```

---

## Коммуникация между сервисами

### Главный транспорт — MQTT

```
┌──────────────┐
│ MQTT Broker  │
└──────┬───────┘
       │
  ┌────┴────────────────────────────────────┐
  │         │           │         │         │
Sponge   Control   Telegram    Web        Tax
(listen) (publish) (listen)  (listen)  (tax-job)
```

**Протокол:** MQTT 3.1.1, опционально TLS
**Аутентификация:** username/password + role-based ACL
**Топики:**

| Топик          | Назначение                      |
|----------------|---------------------------------|
| `vm{ID}/c`     | Событие подключения машины      |
| `vm{ID}/ro`    | Сообщение от робота (FromRobo)  |
| `vm{ID}/cr`    | Ответ на команду                |
| `vm{ID}/*/1s`  | Состояние машины                |
| `vm{ID}/*/1t`  | Телеметрия                      |

Подписка: `+/#` (все машины и топики)

**Режимы MQTT-клиента** (каждая команда использует свои credentials):

| Режим      | Логин     |
|------------|-----------|
| ModeCommand | command  |
| ModeSponge  | ctl      |
| ModeTax     | tax      |
| ModeTelegram| telegram |
| ModeWeb     | web      |

### Сериализация — Protobuf

Используется `github.com/AlexTransit/vender` для определений:
- `vender_api.Command` — команды для машин
- `vender_api.ToRoboMessage` — сообщения роботам
- `vender_api.Order` — информация о заказе
- `vender_api.Telemetry` — телеметрия

### HTTP (Web-сервис)

**Фреймворк:** Gin
**Порт:** 8085
**Аутентификация:** Cookie `auth_user_id`

| Метод | Путь                | Назначение                |
|-------|---------------------|---------------------------|
| GET   | /auth/callback      | Вход (пароль или Telegram)|
| GET   | /auth/logout        | Выход                     |
| GET   | /api/machines       | Список машин              |
| GET   | /api/balance        | Баланс пользователя       |
| POST  | /api/favorite       | Сохранить избранное       |
| GET   | /api/drinks/popular | Популярные напитки        |
| POST  | /api/order/check    | Проверка перед заказом    |
| POST  | /api/order/start    | Создать заказ             |
| GET   | /api/order/ws       | WebSocket (события заказа)|
| GET   | /                   | Фронтенд (index.html)     |

---

## Соглашения по коду

### Именование

```go
// Типы
type Global struct {}       // Экспортируемые — PascalCase
type vmcStruct struct {}    // Внутренние — camelCase
type Teler interface {}     // Интерфейсы — суффикс -er/-or

// Переменные
var reTopic = regexp.MustCompile(...)  // regex — префикс re-
var errTopicIgnore = fmt.Errorf(...)   // ошибки — префикс err-

// Константы
const CmdName = "web"
```

### Файлы

```
cmd-{command}.go            # точка входа команды
web-handler_{entity}.go     # HTTP-обработчики
web-middleware.go           # middleware
types_vars.go               # типы и переменные пакета
{subsystem}_test.go         # тесты
test_util.go                # утилиты для тестов
```

### Обработка ошибок

Используется `github.com/juju/errors`:

```go
// Аннотировать с контекстом
return errors.Annotatef(err, "context: %s", varName)

// Логировать со стеком
g.Log.Errorf("%s\nTrace:\n%s", err.Error(), errors.ErrorStack(err))

// Запись ошибки машины в БД
g.VMCErrorWriteDb(vmid, "описание ошибки")
```

### Логирование

```go
g.Log.Debugf(...)    // отладка
g.Log.Infof(...)     // информация
g.Log.Noticef(...)   // важные события
g.Log.Warningf(...)  // предупреждения
g.Log.Errorf(...)    // ошибки
```

### Graceful Shutdown

```go
g.Alive.Add(1)
defer g.Alive.Done()
<-g.Alive.StopChan()  // ожидать сигнала остановки
g.Alive.Wait()        // ожидать завершения всех горутин
```

---

## Конфигурация

Формат: **HCL** (`venderctl.hcl`)
Поддерживает `include "файл" { optional = true }` для локальных переопределений.

Структура конфига — `internal/state/config.go`

```hcl
db { url = "postgresql://..." }
money { scale = 100 }           # масштаб: 100 = работа в копейках
telegram { ... }
tax { ru2019 { ... } }
cashless { terminalkey = "..." }
tele {
  secrets = "/etc/venderctl/tele.secrets"
  listen "tls://addr:port" { ... }
  connect { url = "tcp://ctl:pass@broker" }
}
include "venderctl-local.hcl" { optional = true }
```

---

## Запуск локально

### 1. Сборка

```bash
./script/build
# Бинарник: ./build/venderctl
# Версия берётся из: git describe --always --dirty --tags
```

### 2. Конфиг

Создать `venderctl.hcl` или `venderctl-local.hcl` с параметрами БД и MQTT.

### 3. Запуск отдельных сервисов

```bash
./build/venderctl sponge     # телеметрия
./build/venderctl web        # web API (порт 8085)
./build/venderctl telegram   # Telegram-бот
./build/venderctl tax        # налоговый процессор
./build/venderctl version    # проверить версию
```

### 4. Управление машинами

```bash
./build/venderctl control ping   VMID
./build/venderctl control report VMID
./build/venderctl control exec   VMID SCENARIO...
./build/venderctl control lock   VMID DURATION
./build/venderctl control qr     VMID TEXT
```

---

## Тестирование

```bash
./script/test
# или напрямую:
go test ./...
go test ./internal/state/...
go test ./cmd/venderctl/tax/...
```

---

## Частые команды

```bash
./script/build                    # собрать
./script/test                     # тесты
go vet ./...                      # статический анализ
golangci-lint run                 # линтер (конфиг: .golangci.yml)
./script/sql-fmt                  # форматировать SQL
./script/remote-venderctl         # управление удалённым экземпляром
go mod tidy                       # очистить зависимости
```

---

## Ключевые зависимости

| Модуль | Назначение |
|--------|-----------|
| `github.com/AlexTransit/vender` | Shared kernel: Protobuf API, типы автоматов |
| `github.com/eclipse/paho.mqtt.golang` | MQTT клиент |
| `github.com/go-pg/pg/v9` | PostgreSQL ORM |
| `github.com/gin-gonic/gin` | HTTP фреймворк |
| `github.com/gorilla/websocket` | WebSocket |
| `github.com/golang/protobuf` | Protocol Buffers |
| `github.com/hashicorp/hcl` | Парсинг HCL-конфига |
| `github.com/juju/errors` | Ошибки со стеком вызовов |
| `gopkg.in/hlandau/passlib.v1` | Хеширование паролей |
| `github.com/temoto/alive/v2` | Graceful shutdown |
| `github.com/coreos/go-systemd` | Интеграция с systemd |
| `github.com/go-telegram-bot-api/telegram-bot-api/v5` | Telegram Bot API |
| `github.com/nikita-vanyasin/tinkoff` | Tinkoff API (QR-платежи) |
| `github.com/AlexTransit/ru-nalog-go` | Российский налоговый API (ФНС) |

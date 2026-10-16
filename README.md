# Vault Chat

Корпоративный командный мессенджер на Go с отдельными сервисами Auth, Chat,
Media и API Gateway. Проект находится в разработке: авторизация доступна через
REST; комнаты, отправка сообщений и история доступны через авторизованный Chat API.

Статус ниже отражает реализацию на 16 октября 2026 года.
[Архитектура](ARCHITECTURE.md) описывает целевую систему; не все её компоненты
уже реализованы.
[Требования и план подготовки к защите](docs/requirements.md) содержат статусы
обязательных компонентов, задачи до коллоквиума и экзамена и критерии готовности.

## Что уже есть

| Компонент | Реализовано | Доступность |
|---|---|---|
| Auth | Регистрация, вход, Argon2id, JWT RS256 и проверка access token | Запускаемый gRPC-сервис; публичный профиль по ID и поиск по началу имени через Gateway |
| Сессии | Хранение в PostgreSQL, refresh rotation, обнаружение повторного использования refresh token, logout и выход со всех устройств | gRPC и REST через Gateway |
| Gateway | REST для Auth, комнат и сообщений Chat, профили пользователей по ID и поиск по имени, проверка токена через Auth, rate limiting по IP и пользователю через Redis, CORS, ограничения тела и времени запроса, recovery, опциональный TLS 1.3 | Запускаемый HTTP-сервис |
| Комнаты Chat | Уникальный личный диалог на пару пользователей с указанием обоих участников, каналы с владельцем, добавление участников владельцем, состав комнаты и чтение только для участников, список комнат пользователя по последней активности с курсором | Go-сервис, отдельная PostgreSQL-БД, авторизованный gRPC и REST через Gateway |
| Сообщения Chat | Неизменяемый текст до 4096 символов, проверка членства, повторная отправка по UUID без дубликатов, история с курсором | PostgreSQL, gRPC и REST через Gateway |
| Контракт Chat | Protobuf-контракт операций с комнатами и сгенерированный gRPC-код | Реализованы все восемь RPC; актор проверяется через Auth |
| Данные | Миграции пользователей и сессий Auth, отдельные миграции комнат, участников и сообщений Chat | Auth и Chat применяют миграции своих отдельных БД при запуске |
| Наблюдаемость | JSON-логи через `slog`, request ID через HTTP → gRPC, `/health` и `/ready` у Gateway | Реализовано для Auth/Chat/Gateway |
| Проверки | Unit-, PostgreSQL integration- и race-тесты, 27 целей фаззинга, проверка `govulncheck`, конфигурации golangci-lint, buf и mockery | Локальные проверки |
| Инфраструктура | Docker Compose с отдельными PostgreSQL для Auth и Chat, Redis, NATS и MinIO; общий `Dockerfile` для Go-сервисов (distroless, non-root), healthchecks и порядок старта | Auth, Chat, Gateway с PostgreSQL и Redis запускаются одной командой; Media ещё нет, NATS и MinIO не используются кодом приложения и вынесены в профили Compose |

Правила и ограничения комнат описаны в [документации Chat](docs/chat/rooms.md).
Chat хранит свои данные отдельно и не обращается к таблицам Auth.

## Чего не хватает до MVP

Цель MVP из архитектуры: **Auth, личные сообщения, каналы, загрузка файлов и
запуск четырёх сервисов через Docker Compose**.

- [x] Авторизация и управление сессиями.
- [x] REST Gateway для Auth.
- [x] Внутреннее ядро комнат и проверки членства/прав владельца.
- [x] Получение профиля по ID в Auth и проверка существования получателя в Chat.
- [x] Поиск пользователей по имени для удобного выбора собеседника.
- [x] Запускаемый Chat-сервис: конфигурация, отдельная БД, применение миграций,
  gRPC-обработчики с проверкой токена и доступом через Gateway.
- [ ] Список комнат и необходимые операции управления участниками.
- [x] Отправка и хранение сообщений, история с курсорной пагинацией,
  ограничения размера и защита от дубликатов при повторной отправке.
- [ ] WebSocket: авторизованные подписки, доставка событий и восстановление
  пропущенных сообщений после переподключения.
- [ ] Media-сервис: загрузка в MinIO, метаданные, скачивание и проверка доступа
  к вложениям, ограничения размера и типа файлов.
- [x] Обновление Go и зависимостей с проверкой `govulncheck`.
- [ ] Контейнеризация Auth, Gateway, Chat и Media; запуск всего MVP одной командой.
  Сделано для Auth, Chat и Gateway: образы, healthchecks и запуск стека одной
  командой Compose. Осталось добавить Media.
- [ ] Сквозной тест: два пользователя входят, создают диалог, обмениваются
  сообщениями и вложением; третий не получает доступ к чужому диалогу.

Для демонстрации обычным пользователям также нужен минимальный веб-интерфейс:
вход, список комнат, переписка и вложения. Фронтенда сейчас нет.

### Ближайший порядок разработки

1. Исправить замечания review и слить контейнеризацию, список комнат, поиск,
   управление участниками и real-time; проверить объединённый Compose-стек.
2. Закрыть требования к кэшу и брокеру: согласовать трактовку с преподавателем,
   подготовить сценарий кэширования данных и сохраняемый поток событий при необходимости.
3. Подготовить воспроизводимое демо, C1/C2, зоны доверия, модель угроз и ADR.
4. К экзамену реализовать и проверить транспортную защиту, балансировку,
   наблюдаемость, нагрузку, отказы и восстановление данных по
   [плану подготовки](docs/requirements.md).
5. Добавить Media и вложения для собственного MVP; обязательность этой функции
   для курса уточнить отдельно.

### Остальные запланированные возможности

Пока отсутствуют организации и команды, полноценный RBAC, TOTP/2FA,
поиск по сообщениям, треды, реакции, редактирование/удаление сообщений,
presence и уведомления. Также не реализованы mTLS между сервисами, E2EE,
аудит административных действий, Prometheus/Grafana, OpenTelemetry и Kubernetes.
Приоритет этих функций после MVP нужно определять отдельно.

## Текущие ограничения

- Logout и отзыв сессий блокируют дальнейшее обновление токенов. Уже выданный
  access token действует до истечения срока, по умолчанию 15 минут.
- Gateway → Auth/Chat и Chat → Auth используют gRPC без TLS. Публичный TLS Gateway включается
  отдельно через сертификат и ключ в конфигурации.
- При недоступности Redis rate limiter пропускает запросы; IP определяется
  по адресу соединения, без поддержки доверенных reverse proxy.
- Chat проверяет токен и существование получателя через Auth. Профиль по ID
  и результаты поиска содержат только ID и имя пользователя. Любой авторизованный
  пользователь может перебрать каталог по префиксам имён; это сдерживают только
  лимит запросов на пользователя и страница не более 50 записей.
- Публичные и приватные каналы пока используют приглашения. Поиск публичных
  каналов и самостоятельное вступление не реализованы.
- `xmake.lua` и команды `xmake`, описанные в регламенте, пока не реализованы.
  Ниже приведены доступные команды Go и вспомогательных инструментов.

`govulncheck` v1.9.0 с Go 1.27.2 и базой уязвимостей от 8 октября 2026 года
не находит достижимых уязвимостей ни в стандартной библиотеке, ни в зависимостях.
До обновления Go и зависимостей проверка на Go 1.23.4 находила 49 достижимых
уязвимостей: 42 в стандартной библиотеке и 11 в модулях `golang.org/x/net`,
`golang.org/x/text`, `google.golang.org/grpc` и `github.com/jackc/pgx/v5`
(четыре уязвимости HTTP/2 затрагивают и `net/http`, и `golang.org/x/net`).
На уровне модулей остаётся только GO-2026-5932: пакет `golang.org/x/crypto/openpgp`
не поддерживается и не имеет исправления, но проект его не импортирует.
Это результат статического анализа; проверку нужно повторять после обновления
зависимостей и перед внешним развёртыванием.

## Запуск всего стека в Docker

Нужны Docker с Compose v2 и OpenSSL; Go для этого режима не нужен.
Команды выполняются из корня репозитория.

```sh
sh scripts/gen-jwt-keys.sh
docker compose -f deployments/docker-compose.yml up -d --build --wait
```

Скрипт один раз создаёт в `secrets/` пару ключей RS256 для разработки и не
перезаписывает существующий закрытый ключ. Открытый ключ всегда выводится из
закрытого: скрипт создаёт его заново вместе с новым закрытым ключом, а также
если он отсутствует или не соответствует закрытому. Вторая команда собирает образы Auth, Chat и
Gateway, поднимает PostgreSQL для Auth и Chat, Redis и ждёт, пока все контейнеры
станут healthy. Auth и Chat применяют миграции при старте.
Media-сервиса пока нет: в `deployments/docker-compose.yml` оставлено место для него.

NATS и MinIO код приложения пока не использует, поэтому они вынесены в
профили Compose `events` и `media` и по умолчанию не запускаются. Их можно
запустить явно: `docker compose -f deployments/docker-compose.yml up -d nats`
или с флагом `--profile events`. Закреплённый в Compose образ
`minio/minio:RELEASE.2025-04-22T22-12-26Z` сейчас не скачивается из Docker Hub;
его нужно заменить вместе с добавлением Media.

Gateway доступен на `http://localhost:8080`; Auth и Chat открыты только внутри
сети Compose. Проверка — те же запросы, что и при локальном запуске ниже:

```sh
curl --fail http://localhost:8080/health
curl --fail http://localhost:8080/ready
```

Полезные команды:

```sh
docker compose -f deployments/docker-compose.yml ps        # состояние и health
docker compose -f deployments/docker-compose.yml logs -f gateway
docker compose -f deployments/docker-compose.yml up -d --build   # пересобрать после изменений
docker compose -f deployments/docker-compose.yml down      # остановить, данные сохраняются
docker compose -f deployments/docker-compose.yml down -v   # остановить и удалить данные
```

Все порты публикуются только на `127.0.0.1`: PostgreSQL, Redis, NATS и MinIO
используют учётные данные для разработки или работают без пароля, поэтому с
других машин они недоступны. Gateway по умолчанию тоже слушает только
loopback; чтобы открыть его в сети (например, для проверки с телефона в
доверенной сети), задайте `GATEWAY_HOST_ADDR=0.0.0.0`.

Если порт на хосте занят, переопределите его переменной окружения: например,
`POSTGRES_HOST_PORT=55432 GATEWAY_HOST_PORT=18080 docker compose ... up -d`.

| Переменная | Сервис | По умолчанию |
|---|---|---|
| `GATEWAY_HOST_ADDR` | Адрес хоста для порта Gateway | 127.0.0.1 |
| `GATEWAY_HOST_PORT` | Gateway (HTTP) | 8080 |
| `POSTGRES_HOST_PORT` | PostgreSQL Auth | 5432 |
| `CHAT_POSTGRES_HOST_PORT` | PostgreSQL Chat | 5433 |
| `REDIS_HOST_PORT` | Redis | 6379 |
| `NATS_HOST_PORT`, `NATS_MONITOR_HOST_PORT` | NATS | 4222, 8222 |
| `MINIO_HOST_PORT`, `MINIO_CONSOLE_HOST_PORT` | MinIO | 9000, 9001 |

Уровни логов задаются через `AUTH_LOG_LEVEL`, `CHAT_LOG_LEVEL` и
`GATEWAY_LOG_LEVEL`, разрешённые CORS-источники — через
`GATEWAY_CORS_ALLOWED_ORIGINS`. Остальная конфигурация задана в Compose-файле
и совпадает с `.env.example`, только вместо `localhost` указаны имена сервисов.

Как устроены образы:

- Один `Dockerfile` для всех сервисов: сервис выбирается аргументом сборки
  `SERVICE`, версия Go — аргументом `GO_VERSION` (по умолчанию 1.27.2; держите
  её равной строке `toolchain` в `go.mod`). Бинарники собираются статически
  (`CGO_ENABLED=0`, `-trimpath`).
- Базовые образы закреплены: сборочный — точной версией Go и Alpine
  (`golang:1.27.2-alpine3.24`), runtime — по digest. Новый digest берётся из
  `docker buildx imagetools inspect gcr.io/distroless/static-debian12:nonroot`.
- Runtime-образ — `distroless/static` без shell, запуск от непривилегированного
  пользователя (uid 65532), корневая ФС только для чтения.
- Healthcheck выполняет встроенный бинарник `cmd/healthcheck`: для Auth и Chat —
  стандартный gRPC health check, для Gateway — `GET /health`. Порядок старта:
  Chat ждёт healthy Auth, Gateway — healthy Auth, Chat и Redis.
- Ключи и сертификаты не попадают в образы: `.dockerignore` исключает
  `secrets/` и `.env`, а ключи JWT монтируются в Auth только для чтения в
  `/run/secrets/`. Скрипт делает файлы ключей читаемыми для пользователя
  контейнера, а каталог `secrets/` — доступным только владельцу. Если ключи
  создавались вручную на Linux, перезапустите скрипт, чтобы исправить права.
- Если запустить стек до создания ключей, на Linux `up` завершится ошибкой
  `bind source path does not exist`, а Docker Desktop создаст на месте ключей
  пустые каталоги и Auth не стартует (`is a directory`). В обоих случаях
  выполните `docker compose ... down`, `sh scripts/gen-jwt-keys.sh` (он удалит
  такие пустые каталоги) и запустите стек снова.
- Gateway в Compose работает по HTTP без TLS; для TLS смонтируйте сертификат
  так же, как ключи, и задайте `GATEWAY_TLS_CERT_PATH` и `GATEWAY_TLS_KEY_PATH`.

## Локальный запуск Auth, Chat и Gateway

Нужны Go **1.26+**, Docker с Compose и OpenSSL. Для race-тестов нужен C-компилятор.
В `go.mod` указан toolchain `go1.27.2`: при стандартном `GOTOOLCHAIN=auto` команда
`go` сама скачает и использует его, если локальная версия Go старше.
Все команды выполняются из корня репозитория.

```sh
cp .env.example .env
sh scripts/gen-jwt-keys.sh
go mod download
docker compose -f deployments/docker-compose.yml up -d postgres chat-postgres redis
```

Здесь из Compose запускается только инфраструктура. Если порт занят, задайте
`POSTGRES_HOST_PORT` и другие переменные из таблицы выше и поправьте адреса в `.env`.
Не запускайте одновременно полный стек в Docker и сервисы через `go run`:
они займут одни и те же порты.

В первом терминале загрузите переменные и запустите Auth:

```sh
set -a
. ./.env
set +a
go run ./cmd/auth
```

Во втором терминале запустите Chat:

```sh
set -a
. ./.env
set +a
go run ./cmd/chat
```

В третьем терминале аналогично запустите Gateway:

```sh
set -a
. ./.env
set +a
go run ./cmd/gateway
```

Приложения читают переменные окружения; `.env` автоматически не загружается.
По умолчанию Auth слушает `:50051`, Chat — `:50052`, Gateway — `:8080`.
Auth и Chat применяют миграции своих БД при запуске. `/ready` требует готовности
обоих gRPC-сервисов; Redis не входит в readiness из-за режима fail-open.

```sh
curl --fail http://localhost:8080/health
curl --fail http://localhost:8080/ready
curl --fail http://localhost:8080/api/v1/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"email":"demo@example.com","username":"demo_user","password":"DemoPassword123"}'
```

Этот запуск обслуживает Auth API, комнаты и сообщения Chat. `cmd/media` пока нет. Ключи и `.env` исключены из Git через `.gitignore`.

## Доступные REST-маршруты

| Метод | Путь | Назначение |
|---|---|---|
| POST | `/api/v1/auth/register` | Регистрация |
| POST | `/api/v1/auth/login` | Вход и получение пары токенов |
| POST | `/api/v1/auth/refresh` | Ротация refresh token и новая пара токенов |
| POST | `/api/v1/auth/logout` | Завершение сессии по refresh token |
| GET | `/api/v1/auth/me` | Личность из access token |
| DELETE | `/api/v1/auth/sessions` | Отзыв всех сессий владельца access token |
| GET | `/api/v1/users` | Поиск по началу имени без учёта регистра: `query` (от 2 символов), `limit` (по умолчанию 20, максимум 50); себя не возвращает; нужен access token |
| GET | `/api/v1/users/{user_id}` | Публичный профиль существующего пользователя; нужен access token |
| GET | `/api/v1/rooms` | Комнаты вызывающего по последней активности: `cursor`, `limit` (по умолчанию 50, максимум 100) |
| POST | `/api/v1/rooms/direct` | Получение или создание диалога с существующим пользователем |
| POST | `/api/v1/rooms/channels` | Создание канала; вызывающий становится владельцем |
| GET | `/api/v1/rooms/{room_id}` | Метаданные комнаты только для её участников |
| PUT | `/api/v1/rooms/{room_id}/members/{user_id}` | Добавление существующего пользователя владельцем канала |
| GET | `/api/v1/rooms/{room_id}/members` | Состав комнаты только для участников: `cursor`, `limit` (по умолчанию 50, максимум 100) |
| POST | `/api/v1/rooms/{room_id}/messages` | Отправка текста; повтор с тем же UUID и текстом возвращает исходное сообщение |
| GET | `/api/v1/rooms/{room_id}/messages` | История участников: `cursor`, `limit` (по умолчанию 50, максимум 100) |
| GET | `/health` | Проверка работы процесса Gateway |
| GET | `/ready` | Проверка готовности Gateway и его зависимостей |

Форматы запросов, ответы и авторизация описаны в
[OpenAPI](docs/api/gateway.yaml).

## Проверки

```sh
go test -race -count=1 ./...
go vet ./...
golangci-lint run
golangci-lint fmt --diff
go build ./...
govulncheck ./...
```

golangci-lint v2 и `govulncheck` должны быть собраны Go 1.27 или новее, иначе
они не загружают пакеты проекта; golangci-lint проверен с версией v2.14.0.
`go install` выбирает toolchain по `go.mod` устанавливаемой утилиты, поэтому
версию Go для сборки `govulncheck` нужно указать явно:

```sh
GOTOOLCHAIN=go1.27.2 go install golang.org/x/vuln/cmd/govulncheck@latest
```

Без переменных тестовых БД PostgreSQL integration-тесты пропускаются.
Для полного прогона создайте две отдельные тестовые БД в запущенном PostgreSQL:

```sh
docker compose -f deployments/docker-compose.yml exec -T postgres \
  createdb -U auth vault_chat_auth_test
docker compose -f deployments/docker-compose.yml exec -T postgres \
  createdb -U auth vault_chat_chat_test

VAULT_CHAT_TEST_DATABASE_URL='postgres://auth:auth@localhost:5432/vault_chat_auth_test?sslmode=disable' \
VAULT_CHAT_TEST_CHAT_DATABASE_URL='postgres://auth:auth@localhost:5432/vault_chat_chat_test?sslmode=disable' \
  go test -race -count=1 ./...
```

Тесты очищают таблицы: используйте только отдельные тестовые БД.
Команды `createdb` нужны один раз при подготовке окружения.

Фаззинг всех целей по 30 секунд:

```sh
for package in $(go list ./...); do
  for target in $(go test "$package" -run '^$' -list '^Fuzz' | awk '/^Fuzz/ {print $1}'); do
    go test "$package" -run '^$' -fuzz="^${target}$" -fuzztime=30s -parallel=2 || exit 1
  done
done
```

Для работы с protobuf нужны `buf`, `protoc-gen-go` и `protoc-gen-go-grpc`:

```sh
cd api/proto
buf lint
buf generate
```

## Документация

- [Требования, статусы и план подготовки к защите](docs/requirements.md).
- [Архитектура и целевые этапы развития](ARCHITECTURE.md).
- [Регламент разработки и review](CONTRIBUTING.md).
- [Конфигурация окружения](.env.example).
- [REST API Gateway](docs/api/gateway.yaml).
- [Правила комнат и границы Auth/Chat](docs/chat/rooms.md).
- [Сообщения, повторная отправка и история](docs/chat/messages.md).

# Vault Chat

Корпоративный командный мессенджер на Go с отдельными сервисами Auth, Chat,
Media и API Gateway. Проект находится в разработке: авторизация доступна через
REST, для комнат реализовано внутреннее ядро. Отправки сообщений пока нет.

Статус ниже отражает `main` после PR #8, на 9 октября 2026 года.
[Архитектура](ARCHITECTURE.md) описывает целевую систему; не все её компоненты
уже реализованы.

## Что уже есть

| Компонент | Реализовано | Доступность |
|---|---|---|
| Auth | Регистрация, вход, Argon2id, JWT RS256 и проверка access token | Запускаемый gRPC-сервис |
| Сессии | Хранение в PostgreSQL, refresh rotation, обнаружение повторного использования refresh token, logout и выход со всех устройств | gRPC и REST через Gateway |
| Gateway | REST для Auth, проверка токена через Auth, rate limiting по IP и пользователю через Redis, CORS, ограничения тела и времени запроса, recovery, опциональный TLS 1.3 | Запускаемый HTTP-сервис |
| Комнаты Chat | Уникальный личный диалог на пару пользователей, каналы с владельцем, добавление участников владельцем, чтение только участниками | Внутренний Go-сервис и PostgreSQL-репозиторий; публичного API ещё нет |
| Контракт Chat | Protobuf-контракт операций с комнатами и сгенерированный gRPC-код | Контракт готов, сервер не подключён |
| Данные | Миграции пользователей и сессий Auth, отдельные миграции комнат и участников Chat | Auth применяет свои миграции при запуске; запуск Chat ещё предстоит |
| Наблюдаемость | JSON-логи через `slog`, request ID через HTTP → gRPC, `/health` и `/ready` у Gateway | Реализовано для работающего стека Auth/Gateway |
| Проверки | Unit-, PostgreSQL integration- и race-тесты, 15 целей фаззинга, конфигурации golangci-lint, buf и mockery | Локальные проверки |
| Инфраструктура | Docker Compose с PostgreSQL, Redis, NATS и MinIO | Контейнеризована инфраструктура; NATS и MinIO ещё не используются кодом приложения |

Правила и ограничения комнат описаны в [документации Chat](docs/chat/rooms.md).
Chat хранит свои данные отдельно и не обращается к таблицам Auth.

## Чего не хватает до MVP

Цель MVP из архитектуры: **Auth, личные сообщения, каналы, загрузка файлов и
запуск четырёх сервисов через Docker Compose**.

- [x] Авторизация и управление сессиями.
- [x] REST Gateway для Auth.
- [x] Внутреннее ядро комнат и проверки членства/прав владельца.
- [ ] API поиска/получения другого пользователя в Auth для проверки получателей.
- [ ] Запускаемый Chat-сервис: конфигурация, отдельная БД, применение миграций,
  gRPC-обработчики с проверкой токена и доступом через Gateway.
- [ ] Список комнат и необходимые операции управления участниками.
- [ ] Отправка и хранение сообщений, история с курсорной пагинацией,
  ограничения размера и защита от дубликатов при повторной отправке.
- [ ] WebSocket: авторизованные подписки, доставка событий и восстановление
  пропущенных сообщений после переподключения.
- [ ] Media-сервис: загрузка в MinIO, метаданные, скачивание и проверка доступа
  к вложениям, ограничения размера и типа файлов.
- [ ] Контейнеризация Auth, Gateway, Chat и Media; запуск всего MVP одной командой.
- [ ] Сквозной тест: два пользователя входят, создают диалог, обмениваются
  сообщениями и вложением; третий не получает доступ к чужому диалогу.

Для демонстрации обычным пользователям также нужен минимальный веб-интерфейс:
вход, список комнат, переписка и вложения. Фронтенда сейчас нет.

### Ближайший порядок разработки

1. Добавить поиск пользователя в Auth, авторизованный gRPC-сервер Chat и REST
   для комнат в Gateway.
2. Реализовать отправку сообщений и историю с проверкой членства в Chat.
3. Подключить WebSocket и восстановление истории при переподключении.
4. Добавить Media и вложения.
5. Собрать воспроизводимый запуск и сквозные проверки MVP.

### Остальные запланированные возможности

Пока отсутствуют организации и команды, полноценный RBAC, TOTP/2FA,
поиск по сообщениям, треды, реакции, редактирование/удаление сообщений,
presence и уведомления. Также не реализованы mTLS между сервисами, E2EE,
аудит административных действий, Prometheus/Grafana, OpenTelemetry и Kubernetes.
Приоритет этих функций после MVP нужно определять отдельно.

## Текущие ограничения

- Logout и отзыв сессий блокируют дальнейшее обновление токенов. Уже выданный
  access token действует до истечения срока, по умолчанию 15 минут.
- Gateway → Auth использует gRPC без TLS. Публичный TLS Gateway включается
  отдельно через сертификат и ключ в конфигурации.
- При недоступности Redis rate limiter пропускает запросы; IP определяется
  по адресу соединения, без поддержки доверенных reverse proxy.
- Внутренний сервис Chat принимает уже проверенную личность. Перед подключением
  публичных обработчиков нужно проверять токен и существование получателя через
  Auth; корректный UUID сам по себе не доказывает существование аккаунта.
- Публичные и приватные каналы пока используют приглашения. Поиск публичных
  каналов и самостоятельное вступление не реализованы.
- `xmake.lua` и команды `xmake`, описанные в регламенте, пока не реализованы.
  Ниже приведены доступные команды Go и вспомогательных инструментов.

## Локальный запуск Auth и Gateway

Нужны Go **1.23+**, Docker с Compose и OpenSSL. Для race-тестов нужен C-компилятор.
Все команды выполняются из корня репозитория.

```sh
cp .env.example .env
mkdir -p secrets
openssl genrsa -out secrets/auth_jwt_private.pem 4096
openssl rsa -in secrets/auth_jwt_private.pem -pubout -out secrets/auth_jwt_public.pem
go mod download
docker compose -f deployments/docker-compose.yml up -d postgres redis
```

В первом терминале загрузите переменные и запустите Auth:

```sh
set -a
. ./.env
set +a
go run ./cmd/auth
```

Во втором терминале аналогично запустите Gateway:

```sh
set -a
. ./.env
set +a
go run ./cmd/gateway
```

Приложения читают переменные окружения; `.env` автоматически не загружается.
По умолчанию Auth слушает `:50051`, Gateway — `:8080`.
Auth применяет миграции своей БД при запуске.

```sh
curl --fail http://localhost:8080/health
curl --fail http://localhost:8080/ready
curl --fail http://localhost:8080/api/v1/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"email":"demo@example.com","username":"demo_user","password":"DemoPassword123"}'
```

Этот запуск обслуживает только Auth API. Бинарников `cmd/chat` и `cmd/media`
пока нет. Ключи и `.env` исключены из Git через `.gitignore`.

## Доступные REST-маршруты

| Метод | Путь | Назначение |
|---|---|---|
| POST | `/api/v1/auth/register` | Регистрация |
| POST | `/api/v1/auth/login` | Вход и получение пары токенов |
| POST | `/api/v1/auth/refresh` | Ротация refresh token и новая пара токенов |
| POST | `/api/v1/auth/logout` | Завершение сессии по refresh token |
| GET | `/api/v1/auth/me` | Личность из access token |
| DELETE | `/api/v1/auth/sessions` | Отзыв всех сессий владельца access token |
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

- [Архитектура и целевые этапы развития](ARCHITECTURE.md).
- [Регламент разработки и review](CONTRIBUTING.md).
- [Конфигурация окружения](.env.example).
- [REST API Gateway](docs/api/gateway.yaml).
- [Правила комнат и границы Auth/Chat](docs/chat/rooms.md).

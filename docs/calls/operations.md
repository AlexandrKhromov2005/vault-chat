# Эксплуатация Calls

Документ описывает Erlang/OTP control plane из [ADR 0005](../adr/0005-call-control.md).
Локальные и production-like Docker проверки не означают, что обновление уже
развёрнуто на VPS. Для rollout нужны два оператора: один выполняет команды,
второй проверяет состояние, алерты и возможность rollback.

## Запуск и конфигурация

Образ собирается отдельно из pinned `Dockerfile.calls`; общий релизный тег
собирает Calls вместе с остальными сервисами:

```sh
scripts/vps.sh build IMAGE_TAG
scripts/vps.sh update IMAGE_TAG
```

Calls получает собственные PostgreSQL, Redis events, mTLS leaf и TURN secret.
`CALLS_PRODUCTION=true` запрещает prototype-адаптеры. `RPC_TLS_MODE=mtls` и
`RPC_TLS_DIR` обязательны на VPS. `CALLS_TURN_URIS` содержит только адреса
собственного coturn; секрет монтируется через `CALLS_TURN_SECRET_FILE` и не
появляется в логах. Backup/restore включает `calls.dump`; старые архивы без
него восстанавливаются как pre-Calls состояние с пустой Calls БД.

Проверки перед включением:

```sh
sh scripts/check-deployment.sh
sh scripts/check-monitoring.sh
scripts/vps.sh mtls status
scripts/vps.sh status
```

## Метрики и алерты

При `CALLS_METRICS_ENABLED=true` Calls слушает только
`CALLS_METRICS_ADDR` (VPS: фиксированный адрес отдельной `internal`-сети
`metrics-calls`, порт 50056). Prometheus обращается с хоста; публичный порт не
открывается. Listener обрабатывает один запрос за раз, читает не более 2 КиБ и
завершает запрос не позже 1 с ожидания ввода; scrape не меняет состояние звонков.

Доступны active/capacity, drain, Redis queue/published/dropped, RPC counters и
histograms, Auth/Chat counters и latency. p50/p95/p99 вычисляются через
`histogram_quantile`. Labels не содержат пользователей, комнаты, SDP, candidates
или IP. JSON formatter подавляет тела OTP reports, чтобы состояние процесса со
сигналами не попало в stdout.

Алерты покрывают потерю scrape, capacity >90%, Redis queue >80%, dependency
failures, RPC p99 >500 мс и drain дольше двух минут. Проверка на учебном стеке:

```sh
python3 tests/ops/monitoring_acceptance.py
```

Сценарий поднимает disposable targets и Prometheus, проверяет изоляцию, probe и
failure alerts и удаляет только ресурсы своего project. Не запускать его на
production Docker.

## Drain и остановка

```sh
docker stop -t 15 vault-chat-calls-1
```

SIGTERM сразу переводит health в `NOT_SERVING`, новые `create` получают
`503 unavailable` и Gateway добавляет `Retry-After: 5`. Команды идущих звонков
не блокируются. Ожидание нулевого active ограничено 9 с, оставляя до 6 с на
acceptor/OTP shutdown внутри 15-секундного Docker budget. При nonzero active Calls всё
равно останавливается; строки и таймеры восстанавливаются из PostgreSQL при
следующем запуске. Установленный P2P media физически не разрывается сервером —
ограничение описано в ADR 0005.

## Recovery и rollback

При падении процесса одного звонка supervisor восстанавливает только его. После
падения VM или контейнера запустить тот же образ; recovery читает все
незавершённые строки и завершает просроченные. Redis потери не восстанавливаются
как Pub/Sub messages: клиенты обязаны сверить state и догрузить signals.

Для отката образа использовать предыдущий проверенный тег:

```sh
scripts/vps.sh update PREVIOUS_IMAGE_TAG
scripts/vps.sh status
```

`update` сначала делает backup. Схема Calls в этом изменении additive, но любой
rollback с несовместимой будущей миграцией запрещён без отдельно проверенного
restore. После rollback проверить create → accept → connected → signal → end,
`up{job="calls"} == 1`, отсутствие новых dependency/internal errors и свободную
capacity. Production configuration verified отмечается только после этих
проверок на самом VPS.

## Воспроизводимый live acceptance

На отдельном локальном stack задать `GATEWAY_URL` и `PROJECT`; сценарий создаёт
только синтетических пользователей и комнаты. При запуске на host с Docker он
также проверяет реальное TURN allocation:

```sh
node tests/ops/calls_live_acceptance.mjs
```

Внутри Node-контейнера передать `INVITE_ALICE`/`INVITE_BOB`, `SKIP_DOCKER=1` и,
если allocation проверяется отдельно, `SKIP_TURN=1`. Не направлять сценарий на
production: регистрация, logout и таймеры меняют состояние сервиса.

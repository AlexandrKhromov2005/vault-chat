# Наблюдаемость одного VPS

Prometheus собирает метрики Auth, Chat, Media и Gateway, Grafana показывает
дашборд, Alertmanager маршрутизирует алерты. Node exporter читает метрики
хоста, blackbox exporter проверяет HTTPS/readiness, S3 и TCP БД/Redis.
Это диагностика одного VPS: полную потерю VPS или самого Prometheus должен
обнаруживать внешний монитор на другой машине/у провайдера.

## Установка

Сначала развернуть приложение по [инструкции VPS](deployment.md) и обновить
все образы до версии с метриками. Затем от root:

```sh
cd /opt/vault-chat
scripts/vps.sh monitoring install
scripts/vps.sh monitoring status
```

Команда создаёт `.env.monitoring` с private ports, отдельный случайный пароль
Grafana, runtime configs, systemd units и textfile timer. Повторная установка
сохраняет пароль. Prometheus/Alertmanager configs проверяются до запуска.
Мониторинг автоматически следует за systemd lifecycle приложения.

UI и метрики не публикуются в интернет. Prometheus, Alertmanager, Grafana
и node exporter слушают host loopback; blackbox и четыре metrics-порта
публикуются только на 127.0.0.1. Docker socket в контейнеры не передаётся.
Node exporter получает readonly host rootfs/proc/sys и host PID/network,
чтобы показывать ресурсы VPS, а не своего контейнера.

## Открыть дашборд

На своём компьютере:

```sh
ssh -L 13000:127.0.0.1:3000 -L 19090:127.0.0.1:9090 -L 19093:127.0.0.1:9093 root@YOUR_PUBLIC_IPV4
```

Открыть `http://localhost:13000/`, пользователь `admin`. Первоначальный пароль прочитать
в своём SSH-терминале командой ниже (если изменён в UI, использовать новый); не отправлять его в общий чат:

```sh
cat /opt/vault-chat/secrets/grafana_admin_password
```

Дашборд **Vault Chat overview** устанавливается автоматически: QPS и ошибки
API/RPC, p95 latency, WebSocket slots, заполнение очереди, потери публикаций,
RSS приложений, CPU/RAM/root disk, возраст backup и срок обслуживаемого TLS.
Обычно нужны два scrape (около 30 секунд), чтобы появились rate-графики.
Первые пять минут история неполная. Prometheus доступен на localhost:19090,
Alertmanager — localhost:19093 через тот же туннель.

## Метрики приложения

`AUTH_METRICS_ADDR`, `CHAT_METRICS_ADDR`, `MEDIA_METRICS_ADDR`,
`GATEWAY_METRICS_ADDR` включают отдельные HTTP listeners. Без переменной
listener не запускается. В Compose overlay внутренний порт 9100 у каждого
контейнера отображается в отдельный loopback-порт VPS 9101–9104.
Публичные `/metrics` и `/debug/pprof` в Gateway не добавляются.

HTTP labels: фиксированный kind, ограниченный method и status. URI/query,
пользователь, комната, токены и содержимое сообщений не используются как labels.
RPC labels содержат зарегистрированный unary method и status code. Health RPC
исключены из RPC-графиков/ошибок; latency WebSocket lifetime не смешивается
с HTTP latency. Gauge WebSocket считает занятые слоты, включая ожидающие auth.

`vault_event_queue_pending/capacity` измеряет очередь committed changes.
Counters `vault_events_dropped_total`, `vault_events_publish_failures_total`,
`vault_events_encoding_failures_total` различают overflow/shutdown, отказ
Redis pipeline и невалидное событие. Redis failure может означать частичную
доставку: успешный Publish не гарантирует получение каждым клиентом.
Гарантии Pub/Sub и восстановление через catch-up не меняются.

## Алерты и уведомления

Правила находятся в `deployments/monitoring/alerts.yml`:

- Недоступные метрики и dependency probes: 2 минуты.
- API 5xx >10% при ≥5 запросах/5m; неожиданные RPC-ошибки >10%.
- Потери real-time событий и очередь >80%.
- RAM <10%, root disk <15%, CPU >90% длительное время.
- Нет успешного backup 26 часов, failed scheduled backup, устаревший textfile.
- Обслуживаемый сертификат истекает <24 часов или certbot job завершился ошибкой.
- Alertmanager не смог доставить уведомление.

По умолчанию уведомления доставляются в локальный receiver и systemd journal:

```sh
journalctl -u vault-alert-journal.service -f
```

Receiver слушает только loopback и сохраняет только status, alertname,
severity, service/job/instance и summary. Уведомления о восстановлении включены.
Это не внешняя доставка: пока не настроен получатель, Telegram/email не приходят.

Для Telegram создать через root-терминал файлы
`secrets/monitoring/telegram_token` и `secrets/monitoring/telegram_chat_id`
(числовой chat ID); токен не включать в команды shell history или Git.
После этого повторить `scripts/vps.sh monitoring install` и отправить тестовый
алерт. Нужны оба файла; частичная настройка отклоняется. Alertmanager читает
токен через `bot_token_file`. Доставка в Telegram появляется только после
явной настройки получателя. SMTP/email в этот блок не входит.

Тест локального notification pipeline без остановки рабочего сервиса:

```sh
python3 - <<'PY'
import datetime, json, urllib.request
now = datetime.datetime.now(datetime.timezone.utc)
alert = {'labels': {'alertname': 'MonitoringPipelineCheck', 'severity': 'info'},
         'annotations': {'summary': 'Controlled monitoring delivery check'},
         'startsAt': now.isoformat(), 'endsAt': (now + datetime.timedelta(minutes=1)).isoformat()}
request = urllib.request.Request('http://127.0.0.1:9093/api/v2/alerts',
    data=json.dumps([alert]).encode(), headers={'Content-Type': 'application/json'})
with urllib.request.urlopen(request) as response:
    print(response.status)
PY
```

Через group_wait (10s) журнал должен содержать `firing`, затем `resolved`.
Если Telegram уже настроен, тест придёт также в выбранный чат.
Для реального service-down теста остановить контейнер отдельного тестового
стека и дождаться алерта; не отключать рабочий Auth ради проверки.

## Эксплуатация и границы

`vault-operations.timer` раз в минуту атомарно обновляет textfile signals.
Проверяется COMPLETE формата 1; незавершённые backup игнорируются.
БД/S3 TCP probe доказывает доступность сокета, а не успешность любого SQL
или бизнес-операции. `/ready` и API/RPC errors дополняют эту проверку.

Метрики хранятся 3 дня, retention size 512 MB; это не жёсткий лимит размера
всего каталога TSDB. Для контейнеров заданы memory limits и ротация логов.
На VPS 4 ГБ оставлять запас для Argon2/БД и следить за реальной RAM.

Backup приложения сохраняет `.env.monitoring`, secrets и metrics overlay.
История Prometheus, Grafana DB и silences Alertmanager не входят в application
backup; дашборд и datasource восстанавливаются из Git, затем повторяется
monitoring install. Внешнее копирование данных приложения остаётся отдельной задачей.
После изменения портов/получателя повторять install; secret files не коммитить.

Проверки: `xmake monitoring` (private exposure + promtool fixtures),
`xmake deployment` (ops/privacy/secrets), `xmake ci` (весь проект).

## Тематические дашборды

В Grafana, кроме общего обзора, доступны три дашборда в папке Vault Chat:

- `/d/vault-chat-services` — запросы, ошибки, задержки, RSS, GC и goroutines.
- `/d/vault-chat-realtime` — WebSocket, очередь, успешные публикации и ошибки Redis.
- `/d/vault-chat-operations` — CPU/RAM/диск, сеть, backup, TLS и активные алерты.

В Prometheus открыть `/consoles/index.html` (через туннель —
`http://localhost:19090/consoles/index.html`). Там те же тематические страницы
с графиками за 15 минут, час или 6 часов и ссылкой на каждый PromQL-запрос.
Обновление раз в 30 секунд; rate использует окно 5 минут. Пустые данные
показаны отдельно от нулевого значения. Приложения показывают только уже
экспортируемые метрики; число пользователей и сообщений в БД не измеряется.
Consoles и JS/CSS поставляются из этого репозитория без сторонних библиотек.
В Prometheus 3 они подключаются явно через `web.console.templates` и
`web.user-assets`; доступ остаётся только через loopback/SSH.

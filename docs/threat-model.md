# Модель угроз Vault Chat

Модель описывает код `main` и развёртывание на одном VPS по
[инструкции](deployment.md). Зоны и потоки совпадают с [C1/C2](c4.md).
Модель звонков подробнее описана в [ADR 0005](adr/0005-call-control.md).
Строка «Проверка» называет тест или скрипт, который подтверждает меру; если
теста нет, это записано явно. Тесты запускает `xmake ci`, если не сказано иное.

## Активы

| Актив | Где хранится | Чем опасна утечка или подмена |
|---|---|---|
| Учётные записи: email, хеши паролей, приглашения | `auth_db` | Захват аккаунта, перебор паролей офлайн |
| Сессии и refresh token | `auth_db`, клиент | Вход от имени пользователя до отзыва |
| Ключ подписи JWT (RS256) | `secrets/` на хосте, только у Auth | Выпуск токена любого пользователя |
| Переписка и состав комнат | `chat_db` | Чтение чужих разговоров |
| Файлы и их метаданные | MinIO, `media_db` | Чтение чужих вложений |
| Звонки и сигналы WebRTC | `calls_db`, Redis | Метаданные разговоров, вмешательство в звонок |
| Push-токены устройств | `auth_db` | Спам уведомлениями на устройство |
| Ключи mTLS сервисов и CA | `secrets/rpc` на хосте | Выдача себя за сервис |
| TURN-секрет | `secrets/turn_secret`, у Calls и coturn | Бесплатный relay для чужого трафика |
| Резервные копии и ключ age | Удалённое хранилище, оператор | Полная копия всех данных |
| Доступность сервиса | Весь стек | Невозможность общаться |

## Нарушители

| Нарушитель | Возможности | Чего не может по предположению |
|---|---|---|
| Анонимный из интернета | Любые запросы к `:443`, `:80`, TURN `:3478` и relay; перебор, большие тела, медленные соединения | Сетевого доступа к внутренним сетям Docker |
| Зарегистрированный пользователь | Действующий токен; запросы к любым ID комнат, файлов и звонков; свой клиент вместо веб-клиента | Получить приглашение без оператора |
| Бывший участник комнаты | Знает ID комнаты и файлов; может хранить старые ответы | — |
| Владелец украденного токена | Access token до истечения или refresh token | Пароль владельца |
| Скомпрометированный сервис | Код и ключи одного контейнера, его сети | Ключи и сети других сервисов |
| Оператор VPS | Root на хосте, все секреты | Это доверенная роль: приложение от неё не защищает |

## Зоны, точки входа и разрешённые потоки

| Зона | Точки входа | Разрешённые потоки |
|---|---|---|
| Public | Gateway `0.0.0.0:443` (HTTPS, WebSocket, веб-клиент); nginx `:80` (ACME и редирект); coturn `3478` и `50000–50099/udp` | Клиент → Gateway REST/WS; клиент → coturn с временными учётными данными; клиент ↔ клиент P2P-звук |
| Internal | gRPC `rpc`; Redis `events`; `profile-cache`; opt-in NATS `push-signals`, воркеры `preview` | Только пары из [матрицы mTLS](mtls.md#матрица-rpc); публикация и подписка в Redis для Auth, Chat, Calls, Gateway; Chat → NATS → Auth |
| Data | PostgreSQL `*-data`, MinIO `objects` | Каждая БД — только её сервис и миграция; MinIO — только Media и `s3-init` |
| Ops | SSH `:22`; Prometheus, Grafana, Alertmanager на loopback хоста; backup timer | Оператор через SSH и туннель; исходящие копии в удалённое хранилище, письма SMTP, FCM, Telegram |

## Угрозы и меры

### Аутентификация и сессии

| ID | Угроза | Мера | Проверка |
|---|---|---|---|
| A1 | Запрос без токена, с испорченным, просроченным или чужим токеном | Gateway требует Bearer, Auth проверяет подпись RS256, тип и срок | `TestAuthenticate`, `TestManager_Validate`, `TestService_ValidateToken_RejectsNonAccessTokens` |
| A2 | Использование токена после выхода или отзыва | Каждая проверка смотрит активную семью сессий в `auth_db` | `TestAccessTokensFollowTheLoginFamilyAndAreRevokedImmediately`, `TestRevokedSessionRejectsAnUnexpiredAccessToken` |
| A3 | Кража и повтор refresh token | Ротация; повтор вне 30-секундного окна для неиспользованного преемника отзывает все сессии пользователя | `TestRefreshToken_ReuseIsStillDetected`, `TestSessionRepository_Rotate_RetryIsReuseWhenNotTheLatest`; позитивный — `TestSessionRepository_Rotate_RetryWithinWindowReturnsTheSuccessor` |
| A4 | Регистрация посторонних | Одноразовое приглашение на конкретный email | `TestPublicRegistrationRequiresInvitation`, `TestInvitationIsEmailBoundAndConsumedExactlyOnce` |
| A5 | Перебор паролей и перечисление аккаунтов | Лимиты входа по адресу и между аккаунтами, при отказе Redis вход закрыт; одинаковая ошибка для неизвестного email и неверного пароля | `TestFailedLoginsFromOneAddressAreLimitedAcrossAccounts`, `TestCredentialRoutesRejectRedisOutage`, `TestService_Login`, `TestInvitedRegistrationDoesNotRevealExistingEmail` |
| A6 | Офлайн-перебор хешей после утечки БД | Argon2id; небезопасные параметры отклоняются | `TestArgon2idHasher_RejectsUnsafeParameters`. Значения `DefaultArgon2idParams` тестом не закреплены |
| A7 | WebSocket без входа или после отзыва | Первый кадр `auth`; сессия проверяется перед каждым событием, по подсказке отзыва и раз в секунду; отставший клиент отключается | `TestRealtime_RejectsConnectionsWithoutValidAuthentication`, `TestRealtimeRevocationClosesIdleConnectionsAndBlocksEvents`, `TestHub_EndsAClientThatFallsBehind`. Лимит входящего кадра тестом не проверен |

### Сообщения и комнаты

| ID | Угроза | Мера | Проверка |
|---|---|---|---|
| C1 | Чтение или запись в чужую комнату | Chat проверяет членство в той же транзакции; посторонний получает `NotFound` | `TestChatMessages_MembershipRetriesAndPagination`, `TestChatMembers_ListedOnlyToMembersInUserOrder` |
| C2 | Удалённый участник сохраняет доступ | Членство проверяется на каждом запросе, без кэша | `TestChatMembers_OwnerRemovesAndAccessEndsImmediately` |
| C3 | Участник управляет чужим каналом | Добавлять и удалять может только владелец | `TestChatChannel_OwnerControlsMembership` |
| C4 | Огромные сообщения и дубли при повторе | Текст до 4096 символов; повтор по `client_message_id` возвращает исходное, другое содержимое — конфликт | `TestMessageText`, `TestChatMessages_ConcurrentRetries` |
| C5 | Утечка внутренних ошибок клиенту | Внутренние ошибки заменяются фиксированным текстом | `TestFromGRPC`, `TestRoomFailures_AreClassifiedWithoutLeakingInternals` |

### Файлы и превью

| ID | Угроза | Мера | Проверка |
|---|---|---|---|
| M1 | Скачивание чужого файла, в том числе после удаления из комнаты | Media спрашивает Chat о текущем членстве при каждом чтении | `TestCompose_PrivateAttachments` (Compose-тест, в `xmake ci` не входит) |
| M2 | Вложение из чужой комнаты или чужой загрузки | Chat проверяет файл в Media: готов, загружен отправителем, в той же комнате | `TestAttachmentScope`, `TestCompose_PrivateAttachments` |
| M3 | Вредный или слишком большой файл | Лимит 10 МиБ, разрешённые типы и проверка содержимого | `TestContent`, `TestImageContent` |
| M4 | Эксплойт в декодере изображений | Opt-in воркер без сети и чужих секретов, бюджет пикселей и памяти, таймаут с убийством группы процессов, проверка ответа | `TestWorkerRejectsMalformedResponses`, `TestWorkerDeadlineKillsProcessGroup`, `TestMakeSkipsImagesAboveThePixelBudget`. Что оригинал не страдает при падении воркера, отдельным тестом не проверено |
| M5 | Доступ ключом Media к чужому бакету | Отдельный пользователь S3 с правами на один бакет | `TestScopedS3CredentialsCannotAccessOtherBuckets` |

### Звонки

| ID | Угроза | Мера | Проверка |
|---|---|---|---|
| K1 | Вызов Calls мимо Gateway | Сервер Calls принимает только сертификат Gateway | `gateway_acl_test` (`calls/test/vault_calls_tls_tests.erl`) |
| K2 | Подделка пользователя или сессии в команде | Calls проверяет токен в Auth; устройство и роль — на каждой команде | `identity_cannot_be_forged` (`calls/test/vault_calls_rpc_tests.erl`), `accept_takes_one_callee_device` (`calls/test/vault_call_tests.erl`) |
| K3 | Звонок вне своего личного диалога | `Chat.GetRoom` с токеном звонящего: комната личная, звонящий в ней | Только заглушка прототипа (`invalid_requests_are_bounded`); production-адаптер `calls/src/vault_calls_chat.erl` отдельным тестом не покрыт |
| K4 | Огромный SDP/ICE | 8192 байта SDP, 512 байт ICE; лишнее отклоняется без записи | `invalid_signal_payloads_leave_no_rows` (`calls/test/vault_call_recovery_tests.erl`) |
| K5 | Звонок продолжается после отзыва сессии или удаления из комнаты | Монитор периодически проверяет сессии и членство и завершает звонок | `monitor_ends_revoked_or_removed_calls` |
| K6 | Перегрузка звонками | Не больше 200 незавершённых звонков, сверх — `503` с `Retry-After` | `the_two_hundredth_call_is_the_last_one`, `TestCallsCapacityHasRetryAfter` |
| K7 | TURN как открытый прокси | HMAC-учётные данные на 600 с только участникам; relay к частным адресам запрещён | `turn_credentials_are_bounded`. `denied-peer-ip` в `deployments/turnserver-entrypoint.sh` тестом не проверен |

### Брокер и push

| ID | Угроза | Мера | Проверка |
|---|---|---|---|
| B1 | Chat читает push-поток или Auth подделывает сигналы | Права NATS по сервисным сертификатам: Chat только публикует, Auth только читает свой consumer | `TestBrokerPermissionsSeparateChatAndAuth`. Публикация Chat в другой subject не проверена |
| B2 | Испорченный сигнал зацикливает доставку | Проверка сигнала; неисправимый завершается `TERM` | `TestConsumerTerminatesMalformedSignals` |
| B3 | Повторная доставка шлёт push дважды | Отметки `(signal, user)` до ACK | `TestConsumerSkipsMarkedRecipients` |
| B4 | Содержимое сообщений уходит в FCM | Сигнал содержит только комнату и получателей; FCM получает data-only сообщение | `TestPublisher_SignalsPushForNewMessagesWithoutContent`, `TestFCMSendsADataOnlySignal` |

### Edge и доступность

| ID | Угроза | Мера | Проверка |
|---|---|---|---|
| E1 | XSS и встраивание страницы | CSP без `unsafe-inline`, данные сервера выводятся как текст, `X-Frame-Options: DENY`, `nosniff` | `TestWebApp_ContentSecurityPolicy`, `TestFiles_IndexIsCompatibleWithTheContentSecurityPolicy`, `TestSecurityHeaders` |
| E2 | Запросы с чужого origin | CORS только для разрешённых источников | `TestCORS` |
| E3 | Большие тела и медленные запросы | Лимиты тела и времени запроса | `TestBodyLimit`, `TestTimeout` |
| E4 | Флуд запросами и WebSocket | Лимиты по IP и пользователю, отдельно для WebSocket и звонков | `TestAuthenticatedUsersBehindOneAddressAreLimitedPerUser`, `TestRouter_WebSocketUpgradesAreRateLimitedPerIP` |
| E5 | Отказ Redis | Чат работает без событий и догружает пропущенное; общие лимиты пропускают запросы, вход закрыт | `TestPublisher_RedisFailureIsLoggedNotReturned`, `TestRealtime_MembersReceiveEventsAndCatchUpAfterReconnecting`, `TestRateLimit`, `TestRequiredRateLimitRejectsUnavailableRedis` |
| E6 | Кэш профилей обходит проверку сессии | Сессия проверяется до чтения кэша; в кэше только `user_id` и `username` | `TestProfileCache_SessionIsCheckedFirst`, `TestCache_StoresOnlyThePublicProjectionForTenMinutes` |

### Внутренние связи и данные

| ID | Угроза | Мера | Проверка |
|---|---|---|---|
| I1 | Скомпрометированный сервис вызывает чужие методы | mTLS с точной идентичностью, матрица методов, отказ по умолчанию, неполная настройка не стартует | `TestMutualTLSOverTCP`, `TestServiceMethodMatrixAndStreams`, `TestRPCPermissions`, `TestFailClosedConfiguration` |
| I2 | Сервис меняет схему или читает чужую БД | Роль `*_app` без DDL; отдельная сеть на каждую БД | `TestRuntimeRoleHasDMLButCannotChangeSchema`, `scripts/check-deployment.sh` |
| I3 | Секреты попадают в Git | Хук сканирует staged-файлы на ключи и известные форматы | `test_private_key_and_local_configuration_are_rejected`, `test_known_credential_format_is_rejected`, `test_staged_secret_cannot_hide_behind_a_clean_working_file` |

### Эксплуатация и копии

| ID | Угроза | Мера | Проверка |
|---|---|---|---|
| O1 | Панели мониторинга доступны из интернета | Prometheus, Grafana, Alertmanager слушают только loopback; метрики — по отдельным internal-сетям | `scripts/check-monitoring.sh`, `test_network_overlap_is_rejected_except_for_own_exact_network` |
| O2 | Утечка копии из удалённого хранилища | Потоковое шифрование age до выгрузки | `test_round_trip_and_failed_producer` |
| O3 | Подменённая или повреждённая копия при восстановлении | Проверка SHA256SUMS, отказ от путей с выходом за каталог и ссылок | `test_corruption_rejected`, `test_traversal_links_and_unexpected_config_rejected` |
| O4 | Несогласованная копия или ложная отметка об успехе | Пауза сервисов на время дампов; при ошибке пауза снимается, копия не отмечается завершённой | `test_failed_dump_unpauses_services_and_never_marks_complete` |
| O5 | Восстановление поверх живых данных | Восстановление только в новый проект без существующих томов | `test_existing_volume_without_labels_is_not_overwritten` |

## Остаточные риски

- **Общий пароль Redis.** Auth, Chat, Calls и Gateway ходят в Redis с одним
  паролем, события не подписаны. Скомпрометированный сервис может отправить
  пользователю поддельное событие чата или звонка; история и права остаются в
  PostgreSQL, поэтому догрузка вернёт настоящие данные.
- **Общие лимиты Gateway открываются без Redis.** При отказе Redis обычный API
  не ограничен по частоте; закрыты только вход и регистрация.
- **IP определяется по адресу соединения.** Доверенных reverse proxy и WAF нет
  (#186); за общим NAT пользователи делят лимит по адресу.
- **DDoS.** Приложение ограничивает запросы и соединения, но не защищает канал
  VPS; внешней защиты нет (#183).
- **Один экземпляр каждого сервиса.** Отказ процесса прерывает обслуживание до
  перезапуска; две реплики Gateway — #180.
- **Нет `kid` в JWT.** Смена ключа подписи завершает все сессии.
- **Нет CRL/OCSP для mTLS.** При компрометации ключа сервиса нужен новый CA.
- **Без шифрования на диске.** БД, MinIO и Redis на хосте не зашифрованы;
  защита — права хоста и шифрование копий.
- **Звонки.** Сервер не разрывает установленный P2P-поток, поток через TURN
  живёт до конца выделения (≤ 600 с). Сервер сигнализации доверенный: проверки
  отпечатков DTLS собеседниками нет.
- **Без E2EE.** Сервер и оператор видят переписку и файлы (#197).
- **Ротация production-секретов** кроме leaf-сертификатов mTLS не
  автоматизирована (#184).
- **Пробелы в тестах:** значения параметров Argon2id, лимит кадра WebSocket,
  production-проверка комнаты звонка, `denied-peer-ip` coturn, сохранность
  оригинала при падении воркера превью, монтирование ключей JWT только в Auth
  и `.dockerignore`.

## Что приложение не защищает

- Хост и оператора: root на VPS получает все секреты и данные.
- Устройства пользователей: вредоносное расширение браузера или заражённый
  телефон видят то же, что пользователь.
- Внешние сервисы: Let's Encrypt, FCM, SMTP relay, удалённое хранилище копий,
  Telegram. Push и письма не содержат переписки, но раскрывают факт события.
- Канал связи VPS и инфраструктуру провайдера.

## Как проверить модель

Для каждой строки выполнить указанный тест: `go test -race -run '<имя>' ./...`
для Go, `rebar3 eunit` в `calls/` для Erlang, `python3 -m unittest` для
`tests/ops` и `tests/dev`, `sh scripts/check-deployment.sh` и
`sh scripts/check-monitoring.sh`. Всё это, кроме Compose-тестов, входит в
`xmake ci`. `TestCompose_PrivateAttachments` запускается против поднятого
стека, как описано в README.

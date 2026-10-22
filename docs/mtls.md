# Внутренний gRPC с mTLS

Публичный HTTPS и внутренний mTLS используют разные CA и ключи. У Auth, Chat,
Media и Gateway отдельные EC P-256 сертификаты с DNS SAN `<service>.vault.internal`
и URI SAN `spiffe://vault-chat/service/<service>`. TLS 1.3 проверяет доверенный CA,
срок и назначение сертификата. Клиент дополнительно проверяет точную идентичность
сервера; сервер требует клиентский сертификат и известный service URI.

JWT, права комнаты и доступ к файлам проверяются как раньше: сертификат
удостоверяет сервис, а не пользователя. Собственный CA и ключ каждого сервиса
доступны только этому сервису; signing key CA не монтируется в контейнеры.

## Матрица RPC

| Вызывающий сервис | Разрешённые прикладные RPC |
|---|---|
| Gateway | AuthService, ChatService и MediaService |
| Chat | Auth.ValidateToken, Auth.GetUser, Media.GetFile |
| Media | Auth.ValidateToken, Chat.GetRoom |
| Auth | Не вызывает другие сервисы |

Все известные сервисы могут вызывать стандартный health Check/Watch.
Остальные вызовы запрещены по умолчанию. Проверка применяется и к unary,
и к streaming RPC, включая загрузку и скачивание файлов. Reflection выключен
при mTLS. Прямой доступ к internal RPC по одному JWT без сертификата невозможен.

## Локальный стенд

```sh
sh scripts/gen-jwt-keys.sh
python3 scripts/gen-rpc-certs.py secrets
docker compose -f deployments/docker-compose.yml -f deployments/docker-compose.mtls.yml up -d --build --wait
```

Для `go run` задать `RPC_TLS_MODE=mtls`, `RPC_TLS_DIR=secrets/rpc/<service>`.
Healthcheck дополнительно использует `RPC_TLS_SERVICE=<service>`.
В режиме mTLS неполная/невалидная конфигурация завершает процесс с ошибкой;
автоматического отката к plaintext нет. Без TLS mode и directory локальный
запуск остаётся plaintext с предупреждением; TLS directory в plaintext отклоняется.
Compose overlay фиксирует mode в `mtls`, отдельный каталог монтируется readonly.

## VPS: включение и обновления

Сначала собрать и загрузить все образы с поддержкой mTLS, обновить приложение
командой `scripts/vps.sh update IMAGE_TAG`. Затем:

```sh
cd /opt/vault-chat
scripts/vps.sh mtls init
scripts/vps.sh mtls enable
scripts/vps.sh mtls status
```

Enable проверяет capability/config новых образов офлайн, делает backup и создаёт
`.env.rpc`. После этого все команды compose через `vps.sh` добавляют mTLS overlay,
включая update, backup, restore и запуск после reboot. Четыре приложения
пересоздаются с существующими volumes, БД/S3/Redis не очищаются. Смена режима
требует короткого перерыва: во время последовательного обновления совместимость
plaintext/mTLS не поддерживается. При ошибке включения mode остаётся mTLS;
исправить конфигурацию и повторить запуск, не снимать защиту автоматически.

CA действует 10 лет, leaf-сертификаты — 90 дней. Повторный init сохраняет
CA, leaf-ключи, JWT и пароли. Root-выпуск выставляет leaf directory 700,
private key 400, владельца 65532; CA directory 700 и signing key 600 root.
Локальный выпуск без root оставляет файлы читаемыми distroless UID внутри
readonly service mount; общий `secrets` должен оставаться 700 на хосте.

## Ротация

```sh
scripts/vps.sh mtls rotate
```

Команда блокирует параллельные операции, проверяет образы, делает backup,
останавливает приложения, выпускает новые leaf-ключи/сертификаты под тем же CA,
пересоздаёт сервисы и проверяет readiness. Обновление не hot-reload: существующие
TLS-соединения заменяются перезапуском. При ошибке выпуска приложения остаются
остановленными; исправить причину, повторить rotate и проверить `/ready`.

Автопродления внутренних сертификатов пока нет. Gauge
`vault_rpc_certificate_not_after_timestamp_seconds` показывает срок загруженного
сертификата, `vault_rpc_mtls_enabled` — режим каждого процесса. Алерт
`InternalCertificateExpiring` срабатывает за 14 дней; plaintext исключён.
Планировать ручную ротацию до истечения, после неё проверить gauge и readiness.
Истёкшие leaf-сертификаты также можно заменить через rotate.

Backup содержит `.env.rpc`, overlay, CA и leaf-ключи. Архив требует закрытого
хранения: доступ к signing key позволяет выпускать доверенные сертификаты.
Restore восстанавливает UID/mode и запускает mTLS; случайный новый CA не создаётся.

CRL/OCSP и отзыв отдельных leaf не реализованы. При компрометации сервисного ключа
одной обычной ротации недостаточно: старый сертификат остаётся доверенным до
истечения. Требуется остановить приложения, сохранить защищённую копию для
расследования, выпустить новый CA и все leaf в отдельном каталоге, заменить
trust bundles и перезапустить весь стек. Ротация CA — отдельная согласованная
операция с проверкой backup/restore; старые доверенные соединения закрыть.
Redis, PostgreSQL и S3 не получают TLS этим изменением; их порты остаются закрытыми.

## Проверки

`internal/shared/rpctls` проверяет реальные TCP handshakes, чужой CA,
неверную service identity, истёкший/отсутствующий сертификат, unary/stream ACL
и fail-closed настройки; URI parsing проходит fuzz. Ops-тесты проверяют
сохранение CA/ключей и ротацию, Compose — изоляцию mounts. Полный стек
проверяется `tests/compose` и живыми web-тестами через публичный Gateway.

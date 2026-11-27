# Звонки на Android

Клиентская часть [ADR 0005](../adr/0005-call-control.md). Код:
`core/calls/Calls.kt`, `WebRtcCallMedia.kt`, `CallForegroundService.kt` и
`ui/chat/CallPanel.kt`.

## Архитектура и границы

- `Calls` живёт в `ChatSession` и использует её authorized REST и ordered
  WebSocket reconnect. `Frame.CallEvent` не доверяет содержимому события:
  состояние и сигналы перечитываются через API. Push также только запускает
  sync/reconcile; Calls публикует для callee signal с room id, без call id/SDP.
- Создание и accept сначала получают RECORD_AUDIO и готовят microphone. Отказ
  разрешения не создаёт HTTP command. Только вкладка/сессия, создавшая или
  принявшая звонок, владеет peer; другие устройства видят state без microphone.
- Команды и сигналы имеют UUID `command_id` и монотонный `sender_seq`; сетевые
  повторы сохраняют их. REST active/signals восстанавливает потери realtime.
  Сигналы, полученные во время асинхронного `media.start()`, сохраняются в
  очередь и проигрываются по порядку после готовности peer, а не отбрасываются.
  Исходящий candidate повторяется с тем же `sender_seq` до подтверждения или
  завершения звонка; offer/answer сохраняют ограниченное число попыток.
- WebRTC закреплён как `io.github.webrtc-sdk:android:150.7871.08` в version
  catalog. AAR загружается из Maven Central; никаких незакреплённых версий или
  сторонних TURN/SFU нет.
- UI показывает четыре этапа звонка и пояснение, чего ждёт пользователь. Тип
  связи `DIRECT`/`RELAY` определяется по selected candidate pair из RTCStats;
  `RECOVERING` приходит из ICE callback, а не из предположения о сети.
- `WebRtcCallMedia` передаёт только audio, использует DTLS-SRTP стека WebRTC,
  не пишет SDP/candidates в логи. Caller делает bounded ICE restart, callee
  отвечает на новый offer. TURN credentials обновляются перед expiry и при
  восстановлении.
- Во время owned connecting/active работает `CallForegroundService` с типом
  `microphone`. End/reject/cancel/logout и server-ended освобождают peer, track,
  audio source/factory и останавливают service. Процессная смерть не оставляет
  приложению скрытого microphone: Android завершает его ресурсы.

## Проверки

```sh
./gradlew :app:testDebugUnitTest --tests 'io.github.alexandrkhromov2005.vaultchat.core.calls.CallsTest'
./gradlew :app:assembleDebug
```

JVM tests используют fake media и MockWebServer: caller/callee lifecycle,
prepare-before-command, сигналы, connected, end cleanup, permission failure и
повтор command id. Они не заменяют device acceptance.

Для device acceptance использовать отдельный Compose stack и synthetic users.
`tests/ops/calls_browser_acceptance.mjs` с `STATE_FILE` создаёт и сохраняет пару
пользователей/комнату (при повторном запуске обновляет Alice access token).
После установки debug и androidTest APK запускаются:

```sh
adb shell am instrument -w -e email 'bob<suffix>@example.test' \
  -e password SyntheticPassword123 \
  io.github.alexandrkhromov2005.vaultchat.debug.test/androidx.test.runner.AndroidJUnitRunner

GATEWAY_URL=http://127.0.0.1:8080 PROJECT=<local-project> \
  STATE_FILE=<private>/browser-state.json SKIP_CALLEE=1 CALL_WAIT_MS=120000 \
  node tests/ops/calls_browser_acceptance.mjs
```

В режиме `SKIP_CALLEE` browser проверяет RTP и ждёт, пока Android сам проверит
свои RTP counters и завершит звонок. Для медленного emulator без KVM тестовые
ringing/connecting/heartbeat timeouts можно увеличить через `ERL_FLAGS`
(`-vault_calls ...`) только в disposable stack, не меняя production defaults.

### Два Android-эмулятора

`tests/ops/android_calls_acceptance.py` создаёт новых synthetic-пользователей,
запускает caller и callee на двух устройствах и требует `OK (1 test)` на обоих.
Каждая сторона проверяет рост отправленного и полученного RTP, завершение и
отсутствие active call на сервере. `Process crashed` и JUnit failure не считаются
успехом даже при нулевом коде `am instrument`. Debug APK собирается с
`-Pvaultchat.callsForceRelay=true`, поэтому проверяется именно путь через TURN;
обычные debug/release сборки сохраняют `IceTransportsType.ALL`. Фикстура задаёт
`TURN_RELAY_IP=127.0.0.1`: иначе Coturn с wildcard relay address может выбрать
LAN-интерфейс хоста, и mapping `10.0.2.2/127.0.0.1` не применится к candidates.
Для диагностики без mapping `TURN_PUBLIC_IP`, `TURN_PRIVATE_IP` и `TURN_RELAY_IP`
можно задать равными фактическому адресу хоста, а этот же адрес передать в
`CALLS_TURN_URIS`; такой стенд доступен из соответствующей локальной сети и должен
оставаться disposable.

Стенд предназначен только для отдельного worktree и локального Docker. Нужны
Android SDK с `platforms;android-37.0`, emulator, `platform-tools` и образ
`system-images;android-26;default;x86_64`. Два AOSP AVD без Google Services
используют до 3 GiB RAM; без KVM загрузка и ICE gathering могут занимать минуты.
Не запускайте его против production и не копируйте настоящие ключи.

```sh
git worktree add --detach .tools/worktrees/android-calls <commit>
cd .tools/worktrees/android-calls
sh scripts/gen-jwt-keys.sh
openssl rand -out secrets/turn_secret -hex 32
chmod 755 secrets
chmod 644 secrets/auth_jwt_private.pem secrets/auth_jwt_public.pem secrets/turn_secret

export PROJECT=vault-android-e2e
export POSTGRES_HOST_PORT=15432 CHAT_POSTGRES_HOST_PORT=15433
export CALLS_POSTGRES_HOST_PORT=15434 MEDIA_POSTGRES_HOST_PORT=15435
export REDIS_HOST_PORT=16379 MINIO_HOST_PORT=19000 MINIO_CONSOLE_HOST_PORT=19001
export GATEWAY_HOST_PORT=8080 TURN_PUBLIC_IP=10.0.2.2 TURN_PRIVATE_IP=127.0.0.1
export CALLS_TURN_URIS='turn:10.0.2.2:3478?transport=tcp'
docker compose --project-name "$PROJECT" --env-file /dev/null \
  -f deployments/docker-compose.yml -f tests/ops/fixtures/android-calls/build.yml \
  up -d --build --wait

export ANDROID_HOME=/absolute/path/to/android-sdk
tests/ops/android_calls_emulators.sh
adb -s emulator-5554 wait-for-device
adb -s emulator-5556 wait-for-device
# Дождитесь sys.boot_completed=1 на обоих устройствах.
python3 tests/ops/android_calls_acceptance.py --project "$PROJECT" --build
```

Для повторного прогона на тех же тёплых AVD без изменения APK можно использовать
`--no-install`; по умолчанию APK всегда переустанавливаются. Первый запуск
instrumentation после загрузки без KVM может занимать несколько минут.

Если Docker port forwarding на хосте не работает, вместо обычного `up` используйте
host networking только для disposable-стенда. Все БД, Redis и MinIO слушают
loopback; Calls gRPC в текущей реализации слушает все интерфейсы, поэтому стенд
нельзя оставлять запущенным в недоверенной сети.

```sh
docker compose --project-name "$PROJECT" --env-file /dev/null \
  -f deployments/docker-compose.yml -f tests/ops/fixtures/android-calls/build.yml \
  -f tests/ops/fixtures/android-calls/host-network.yml up -d --wait \
  postgres chat-postgres calls-postgres media-postgres redis profile-cache minio \
  auth-migrate chat-migrate media-migrate calls-migrate s3-init auth chat media calls coturn
tests/ops/android_calls_host_gateway.sh
```

Остановите только ресурсы этого стенда:

```sh
adb -s emulator-5554 emu kill
adb -s emulator-5556 emu kill
docker compose --project-name "$PROJECT" --env-file /dev/null \
  -f deployments/docker-compose.yml -f tests/ops/fixtures/android-calls/build.yml \
  down -v
```

24 ноября 2026 на API 26 emulator без KVM, после исправления TURN mapping и
синхронизации acceptance, два подряд Android ↔ browser прогона завершились
`OK (2 tests)`: Android и browser подтвердили рост sent/received RTP и корректный
end без crash. Instrumented-тест не запускает Activity, поэтому звонок также
выполнен без видимого UI с foreground microphone service.

API 26 foreground-service crash, offer/answer mutex deadlock и остававшийся
`active` после 404 на `end()` воспроизведены, покрыты regression и исправлены.
Потеря offer/candidate, пришедших до завершения асинхронного `media.start()`,
также воспроизведена unit-регрессией: теперь они сохраняются и проигрываются
после готовности media в исходном порядке.
27 ноября двухэмуляторный forced-relay прогон пока не прошёл: TURN/TCP на одном
AVD не завершил gathering; TURN/UDP через loopback mapping дал candidates и
connectivity checks, но ICE завершился `FAILED`; при LAN relay candidate caller
неоднократно получал client network timeout. Во всех этих прогонах RTP нулевой,
поэтому работающий Android ↔ Android звонок ими не подтверждён.
На двух физических клиентах 25 ноября дополнительно воспроизвели истечение ringing
через 30 секунд: `404` при refresh/accept оставлял старую панель, которая затем
блокировала новый входящий. Теперь `404` завершает звонок и освобождает media,
а reconcile переходит к актуальному входящему; три regression-сценария это покрывают.
На физическом Galaxy S23 Ultra с Android 17 release 0.2.0 дополнительно
воспроизвёл SIGABRT при инициализации WebRTC: R8 удалил `WebRtcClassLoader` и
`JniZero`. В 0.2.1 keep-rules и проверка APK исправляют это; установка поверх
0.2.0, исходящий звонок и отмена прошли без crash или оставшегося microphone
service. Собеседник вызов не принимал, поэтому remote RTP здесь не проверялся.

Cold start с delayed/missed push, process death, реальный network handover и
forced TURN именно на Android остаются непроверенными; поэтому #163 пока не
закрывается.

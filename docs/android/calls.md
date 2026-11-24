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

24 ноября 2026 на API 26 emulator без KVM, после исправления TURN mapping и
синхронизации acceptance, два подряд Android ↔ browser прогона завершились
`OK (2 tests)`: Android и browser подтвердили рост sent/received RTP и корректный
end без crash. Instrumented-тест не запускает Activity, поэтому звонок также
выполнен без видимого UI с foreground microphone service.

API 26 foreground-service crash, offer/answer mutex deadlock и остававшийся
`active` после 404 на `end()` воспроизведены, покрыты regression и исправлены.
На физическом Galaxy S23 Ultra с Android 17 release 0.2.0 дополнительно
воспроизвёл SIGABRT при инициализации WebRTC: R8 удалил `WebRtcClassLoader` и
`JniZero`. В 0.2.1 keep-rules и проверка APK исправляют это; установка поверх
0.2.0, исходящий звонок и отмена прошли без crash или оставшегося microphone
service. Собеседник вызов не принимал, поэтому remote RTP здесь не проверялся.

Cold start с delayed/missed push, process death, реальный network handover и
forced TURN именно на Android остаются непроверенными; поэтому #163 пока не
закрывается.

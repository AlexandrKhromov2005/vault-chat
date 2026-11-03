# Android client: notifications and push

Stage 7 of the [plan](plan.md). The server side is [Push notifications](../push.md):
Auth sends a data-only FCM message `{"type":"sync","room_id":"…"}` and
stores one device token per login.

## Behaviour

- **Notifications.** `Notifier` shows the unread messages of others that no
  notification has shown yet, one notification per room (up to its last 5
  messages), while the app is not on screen. On screen, new messages are
  only marked as shown, so that they do not pop up when the app leaves the
  screen. The table `notified` keeps the newest shown sequence of each room;
  messages up to the room's last read one are never shown, and the history
  of a room's first load counts as read, except in a dialog new to the device
  after its first room list (see [sync](sync.md)). A room coming on screen, also when
  the user returns to it, removes its notification; tapping a notification
  opens the room, only for the account it was posted for.
- **End of a session.** Nothing of an account stays visible or actionable
  after it signs out or its session is revoked: the session stops posting, all
  of its notifications and a room waiting to open are removed. The notification
  of a room the user has left goes at the next check.
- **Names.** A first-time sender's profile is read after the message is
  stored, so a room with a sender whose name is not stored yet waits for it. The
  background sync waits up to 10 seconds and then shows what it has, so a name
  that cannot be read delays a notification only until the next sync at most.
- **Privacy.** A notification is `VISIBILITY_PRIVATE` with a public version
  that says only "new message", so the lock screen follows the user's system
  choice: with "hide sensitive content" it shows that public version, and with
  "show all content", the default on many phones, it shows the sender and the
  text. Text and names come through the API, never through FCM.
- **Background sync.** `SyncWorker` runs a sync of the current session and
  then `Notifier.check()`: at once (expedited) when a push arrives, and every
  15 minutes with a network as a fallback for lost pushes and for builds
  without push. A push only wakes the sync; a forged or lost push can neither
  show nor hide a message. It is ignored while this device's WebSocket is live,
  which has the message already. A push never cancels a running sync, which may
  have passed the server before the message: one more sync waits behind it, and
  only one, so a stream of pushes costs at most one sync after another.
- **Push.** `Push` starts Firebase when the build has a project configured,
  gets the installation's token and registers it with
  `PUT /api/v1/push/token` after every sign-in and whenever FCM changes it. A
  failed registration is tried again on the next start; the periodic sync
  covers the gap. Signing out revokes the login, and the server deletes its
  token (docs/push.md); the client sends nothing to the server, but deletes the
  installation's token at FCM, tried again at the next start if that fails. So
  after a logout the server could not hear of, the token left there wakes
  nothing, and FCM's answer makes the server drop it. The next sign-in gets a new
  token.
- **Permission.** On Android 13 and later the app asks once for the
  notification permission when the chat opens; refused, it shows nothing and
  keeps working.

## Configuration

The Firebase project is set outside the repository, like the signing key,
in `~/.gradle/gradle.properties`:

```properties
vaultchat.firebase.appId=1:123456789:android:abcdef
vaultchat.firebase.apiKey=...
vaultchat.firebase.projectId=...
vaultchat.firebase.senderId=123456789
```

The values are those of the Android app registered in the Firebase project
(`google-services.json`: `mobilesdk_app_id`, `current_key`, `project_id`,
`project_number`). Without them the build has no push; with only some of them
the build fails. The server needs the same project's service account in
`AUTH_PUSH_FCM_CREDENTIALS_FILE`.

## Not in this stage

- A foreground service that keeps the WebSocket open in the background
  ("persistent connection" mode of the plan).
- Replies and mark-as-read from the notification.

## Tests

`NotifierTest`: new messages of others are shown once, history and own
messages never; on screen nothing is shown and nothing pops up later; a read
room is not shown; a replacing notice keeps what is unread; the first message
of a new dialog shows while the first load of a device stays silent; a failed
mark does not alert again; a room that is gone loses its notification.
`NotificationLifetimeTest`: sign-out and revocation leave no notification and
no pending room, through the real container; a tap opens a room only for its
account. `ChatSessionsTest`: an ended session posts nothing. `OldSqliteTest`:
the notification queries and upserts on an engine older than API 26's, and a
static check of all queries for newer syntax. `PushSignalTest`, `SyncWorkerTest`
and `PushTokensTest`: the push decision, a running sync left alone with one more
behind it, tokens across logins. Not covered: FCM delivery and token deletion
at FCM, expedited scheduling on a real device, and the notification's look,
checked by hand on a device with a Firebase project.

A partial Firebase configuration failing the build is checked with
`./gradlew :app:help -Pvaultchat.firebase.appId=1:2:android:3 -Pvaultchat.firebase.apiKey= -Pvaultchat.firebase.projectId= -Pvaultchat.firebase.senderId=`.

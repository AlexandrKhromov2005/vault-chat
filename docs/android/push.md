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
  of a room's first load counts as read. Opening a room removes its
  notification; tapping a notification opens the room.
- **Privacy.** On the lock screen a notification says only "new message";
  the text needs the phone unlocked (`VISIBILITY_PRIVATE` with a public
  version). Text and names come through the API, never through FCM.
- **Background sync.** `SyncWorker` runs a sync of the current session and
  then `Notifier.check()`: at once (expedited) when a push arrives, and every
  15 minutes with a network as a fallback for lost pushes and for builds
  without push. A push only wakes the sync; a forged or lost push can neither
  show nor hide a message.
- **Push.** `Push` starts Firebase when the build has a project configured,
  gets the installation's token and registers it with
  `PUT /api/v1/push/token` after every sign-in and whenever FCM changes it. A
  failed registration is tried again on the next start; the periodic sync
  covers the gap. Signing out revokes the login, and the server deletes its
  token (docs/push.md); the client sends nothing.
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
`project_number`). Without them the build has no push. The server needs the
same project's service account in `AUTH_PUSH_FCM_CREDENTIALS_FILE`.

## Not in this stage

- A foreground service that keeps the WebSocket open in the background
  ("persistent connection" mode of the plan).
- Replies and mark-as-read from the notification.

## Tests

`NotifierTest`: new messages of others are shown once, history and own
messages never; on screen nothing is shown and nothing pops up later; a
read room is not shown. Not covered: FCM delivery, WorkManager scheduling
and the notification's look, checked by hand on a device with a Firebase
project.

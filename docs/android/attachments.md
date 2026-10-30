# Android client: attachments

Design of stage 6 of the [plan](plan.md): files in messages, received and
sent. Sending builds on the outgoing queue of [stage 4](send.md); the server
side is described in [Private room attachments](../media/files.md) and needs
no change.

## What the server guarantees

Checked in the code (`internal/gateway/handler/media.go`,
`internal/media/validator`, `internal/chat/service/attachments.go`), not
only in the documentation:

- `POST /api/v1/rooms/{room_id}/files` takes one multipart part named
  `file` with a file name and answers `201` with the file's metadata. A
  file is stored only after the whole body arrived: the Gateway passes it
  to Media only once the part and the closing boundary were read, and
  cancels Media's stream otherwise, so Media never publishes it. An upload
  has no idempotency key; one whose answer is lost may have stored a file
  that nothing references.
- At most 2 uploads of one user at once (`429` with `Retry-After: 1`), 8 per
  Gateway (`503`). Files above 10 MiB get `413`, unsupported or broken
  content and invalid names `400`, non-members `404`. The type comes from
  the content: `image/png`, `image/jpeg`, `application/pdf`,
  `text/plain; charset=utf-8`. Names are valid UTF-8 of at most 255 bytes
  without control or format characters, `/`, `\`, `:`, surrounding
  whitespace or a trailing dot. Names in answers always end with the
  extension of the detected type.
- `attachment_ids` of a message: up to 5 distinct files uploaded by the
  sender to this room. Chat checks every ID with Media before it looks for
  a retry under the key, so a retry needs the files to be readable: a
  sender who lost membership gets `400` (`invalid attachment`), not `404`.
  Media down: `503`. A retry with the same key compares the text and the
  ordered IDs; any change is `409`.
- `GET /api/v1/files/{id}` and `/content` check current membership on
  every request (`404` otherwise). Content comes with the stored type,
  `Content-Length` equal to the metadata's size, `Content-Disposition:
  attachment` and `no-store`. Files are immutable; nothing deletes them.
- File requests share the Gateway's rate limit with the rest of the API
  (per user and IP). Transfers have a budget of 5 minutes and must make
  progress every 30 s.

## Components

| Component | Role |
|---|---|
| `files` table | Metadata of files, from uploads and `GET /files/{id}` |
| `outgoing_files` table | Files of queued messages: the staged copy and, once uploaded, the file ID |
| `Attachments` | Loads metadata and content of a session's files, keeps the download cache |
| `Staging` | Copies picked files into private storage and checks them |
| `Outbox` | Uploads a queued message's files, then sends it |
| `FileUpload` | The multipart body; marks the moment the server may have stored the file |
| UI | Pickers, the composer's files, files in bubbles, the image viewer, open and save |

## Trust boundaries

- **Picked files.** Read once through the content URI the picker granted,
  copied into the app's private storage and checked there: 1 byte to
  10 MiB, a PNG, JPEG or PDF signature or UTF-8 text without NUL, so that
  a HEIC photo or a document of another type is refused before it is
  queued. The display name is made to fit the server's rules (control and
  format characters dropped, `/ \ :` replaced, at most 255 bytes, `file`
  when nothing is left). The server decides in the end; its `400` fails the
  message.
- **Gateway answers.** An upload's answer must name this room, this user
  and the staged size; metadata must name the requested file and the
  message's room, a size of 1 byte to 10 MiB and a name of at most 255
  bytes. Content must not exceed the metadata's size, and must have exactly
  it. Anything else is an invalid response and is not stored.
- **File names and types from the server** are untrusted. Names are shown
  as plain text with bidirectional and other format characters removed,
  and never used as paths: a cached file is stored under its file ID, in a
  directory with a name made from the metadata's name and the extension of
  its type. Only `image/png` and `image/jpeg` content whose first bytes are
  a PNG or JPEG signature is decoded, by the platform's decoder through
  Coil, scaled to the size shown. PDF and text are never rendered by the
  app: they are opened in another app chosen by the user.
- **Other apps.** They get a `content://` URI of a cached copy from a
  `FileProvider` that is not exported and serves only the attachment
  cache, with a temporary read grant. The access token never leaves the
  app, not even in a URL.
- **Session binding.** Requests go through the session like all others.
  Staged copies and cached content live in directories named after the
  session's database (`ChatDatabases.fileName`), are deleted on sign-out
  and when a session ends, and are swept with the database files of
  sessions that are no longer open.

## Stored state

Database version 6 adds two tables; without a released version the old
schema is dropped and rebuilt.

`files`: `file_id` (primary key), `room_id` (deleted with the room),
`owner_id`, `name`, `content_type`, `size`, `created_at`. Files are
immutable, so a row never changes.

`outgoing_files`: `client_message_id` and `position` (primary key, the
order of `attachment_ids`; follows a new key after a conflict, deleted with
the row), `name`, `type` (sniffed), `size`, `staged` (the copy's name in the
staging directory), `file_id` (null until uploaded) and `maybe_stored`
(the last attempt's body may have reached the server whole, and no answer
arrived). The queue row keeps its text, which may now be empty.

Staged copies are in `noBackupFilesDir/outbox/<session>/`, cached content
in `cacheDir/attachments/<session>/<file_id>/<name>`. A staged copy is moved
into the cache once its upload succeeds, so the sent message shows it
without downloading it. Staged copies that no queued message references
(a draft left behind by a killed process) are deleted when the session
starts.

The rule "shown exactly once" of [send.md](send.md) is unchanged: a stored
message of this user with the row's key and text resolves the row. The
answer to a send must also carry the row's file IDs in order.

## Receiving

- A bubble shows its message's files below the sender's name and above the
  text: images as thumbnails (one image 260 by 200 dp, more in a grid of
  squares, cropped, so that their content arriving never changes the
  bubble's size), other files as a row with an icon, the name and the size.
  A file whose metadata is not loaded yet shows as a row with a progress
  indicator; a picture takes its place once its metadata is known, which
  happens once per file.
- The bubble speaks as one to accessibility services, as before; its files
  are opened through its actions ("Open «name»"), and one action loads
  again the files that failed.
- Metadata of a file is loaded when its bubble is shown and stored for
  good. Thumbnails are downloaded when they are shown; PDF and text only
  when the user opens or saves them, with a progress indicator.
- `404` shows "file unavailable" and is not asked again during the
  session's life, except when the user taps it. Other failures show a
  retry; a new network retries what is on screen.
- Tapping an image opens a viewer on the whole screen with Save and Open
  in. Tapping another file downloads it if needed and offers Open and
  Save. Save uses the system's document creation (`CreateDocument`), so no
  storage permission is needed; Open hands the cached copy to another app
  (`ACTION_VIEW`, type from the allowlist).

## Sending

- The composer's clip button offers Photo (the system Photo Picker, images
  only) and File (`OpenMultipleDocuments` with the four types). Neither
  needs a permission. Up to 5 files per message; picked files are copied
  and checked one after another, so that they keep the order they were
  picked in, which is the order of `attachment_ids`, and appear above the
  text field, a picture with its thumbnail, with a button to remove it.
  Files of a draft that is not sent are deleted when the conversation
  closes.
- Send queues the text, which may be empty, with the files in one
  transaction. The bubble appears at once with the files and an upload
  progress; sending goes on in the background (OutboxWorker) as for text.
- The room's order holds: the message's files are uploaded when it is the
  room's next row, one file at a time and one upload at a time for the
  whole session. Then the message is sent with the file IDs in order. A
  file uploaded once is never uploaded again for this message, so the IDs
  under a key never change and a repeated send stays identical.
- **Repeating an upload.** The multipart body is written by the app; just
  before its closing boundary leaves, `maybe_stored` is set in the
  database. Without the closing boundary the server cannot store the file,
  so a failure while the flag is not set is repeated after the backoff,
  like a send. With the flag set and no answer, the outcome is unknown: the
  message fails, and the user decides (the server's guidance not to repeat
  an ambiguous upload by itself). A process killed during an upload finds
  the flag the same way. Any answer clears it, since an answer tells the
  outcome.

| Upload answer | Outcome | Action |
|---|---|---|
| `201` matching | stored | file ID stored, copy moved to the cache, next file |
| connection failed before the closing boundary | not stored | repeated after the backoff |
| `401` | not stored | the session renews the token and repeats once |
| `429` | not stored | everything waits for `Retry-After`, then repeats |
| `503` | not stored (capacity, Media down) | repeated after the backoff |
| no answer after the closing boundary, `408`, `500`, `502`, `504`, invalid answer | unknown | `FAILED` (`upload`) |
| `400`, `413` | rejected | `FAILED` (`file`) |
| `403`, `404` | rejected | `FAILED` (`no_access`) |

  Sending the message then follows [send.md](send.md); its `400` (`invalid
  attachment`) is `FAILED` (`invalid`).
- **Retry** of a failed message uploads only the files without an ID; after
  `409` it takes a new key and keeps the IDs; the key changes in place, so
  the message's files follow it. **Cancel** is offered while a file of the
  message is not uploaded: such a message has never been sent, so deleting
  it is safe; a running upload is cancelled. **Delete** stays for failed
  messages whose files are all uploaded.
- A queued message of files alone is previewed in the room list as an
  attachment. A message that leaves the queue keeps its files on screen
  until its stored copy shows them (`keptForHandoff`), and an uploaded
  file shows from the metadata and the copy its upload stored, so the
  hand-over changes nothing.

## Background

Uploads are part of sending, so the WorkManager job of stage 4 also uploads.
A run lasts at most 8 minutes; five files of 10 MiB on a slow network may
need more, and the next run goes on with the files not yet uploaded. The job
is not a foreground service: it does not need the notification permission,
and a stopped upload resumes. A long upload in the foreground of a screen
that is closed keeps running, as it runs in the session's scope.

## Resource budgets

| Budget | Value | Why |
|---|---|---|
| Gateway rate limit per user and IP | 120/min, burst 30, shared | File requests (metadata, downloads, uploads) keep to their own 30/min, burst 5, next to sync's 60/min and sending's 30/min: 120 together. A file shown on screen goes at once while the budget is in debt of less than its burst, so that a screen of pictures loads without waiting; uploads wait for a token; a tap takes one at once |
| Concurrent metadata reads and downloads | 2 | Pictures of one screen arrive in parallel without crowding out other requests |
| Concurrent uploads | 1 per session | The Gateway allows 2 per user; one is left for another device |
| File size | 10 MiB | Server limit, checked before queuing and while downloading |
| Files per message | 5 | Server limit |
| Staged copies | at most 5 per draft and 5 per queued message, 10 MiB each | Chosen by the user's own hand |
| Download cache | 100 MiB per session, least recently used first; the system may clear it any time | A cleared file is downloaded again |
| Request deadline | 5 minutes, progress within 30 s (writing and reading) | The Gateway's transfer budget |
| Decoded thumbnails | scaled to the size shown, kept in Coil's memory cache | A 40-megapixel image is never decoded whole |

## Failure behaviour

| Failure | Behaviour |
|---|---|
| No network while sending | The message waits with its files, as text does |
| Connection lost during an upload | Repeated after the backoff from the first byte |
| Connection lost after the whole file was sent | The message fails (`upload`); Retry uploads the file again; the first copy may stay on the server unused |
| Process killed during an upload | The next run repeats it, or fails it if the closing boundary had left |
| A picked file is too large, empty or of another type | Refused when picked, with a message; nothing is queued |
| The server rejects a file | The message fails (`file`); Delete or Retry |
| Media down | Uploads and sends with files repeat after the backoff; text messages are not affected |
| Metadata or content not available (`404`) | "File unavailable"; the message is shown otherwise |
| Download broken or larger than announced | Discarded; the file shows a retry |
| No app opens the file | A message says so; Save still works |
| Access lost to a room | Its files fail like its messages; metadata goes with the room |
| Sign-out, session end | Staged copies and the cache are deleted with the database |

## Acceptance tests

JVM tests with Robolectric, a fake Gateway that stores files and messages
like the real one, and an `okhttp` `MockWebServer` for the transfer rules:

1. A message with files: uploaded in order, sent with the IDs in order, the
   answer checked; the row is resolved by its echo; the files' metadata and
   the copies are in place without a download.
2. A connection broken before the closing boundary repeats the upload
   after the backoff; broken after it fails the message without a repeat;
   the flag is set exactly before the closing boundary leaves.
3. A process killed during an upload: a new session repeats it, or fails it
   if the flag is set.
4. Answers: `401` repeats once, `429` pauses everything, `503` repeats, `400`
   and `413` fail with `file`, `404` with `no_access`, `504` with `upload`.
5. Retry after `upload` uploads only the files without an ID; after `409`
   it keeps the IDs under a new key; a send answer with other IDs fails the
   row.
6. Cancel deletes a message whose files are not all uploaded and stops its
   upload; a message with every file uploaded cannot be cancelled.
7. Order: a message with files holds the next text of its room; another
   room is not held.
8. Staging: size limits, signatures, UTF-8 text, names made to fit the
   server's rules; orphaned copies are swept when the session starts.
9. Metadata: stored once, a file of another room or a bogus size is
   refused, `404` is remembered, loads are shared and keep to the budget and
   the concurrency.
10. Downloads: written whole or not at all, larger or shorter than
    announced is discarded, the cache keeps to its size, sign-out deletes it.
11. Only PNG and JPEG with a matching signature are decoded; names are shown
    without format characters; the `FileProvider` serves only the cache and
    is not exported (checked by its configuration: FileProvider compares
    paths with `/` and cannot run on a Windows build machine).
12. Compose: a bubble with pictures and files keeps its size while they
    load; an outgoing message shows its waiting file and Cancel; the
    hand-over to the stored message keeps the files on screen.
13. The conversation's draft (its model): picked files are checked, kept in
    the order they were picked, at most 5; a removed file and an unsent
    draft are deleted, while files of a message being queued stay.
14. The upload body: one part named `file` with a quoted name, the mark set
    after everything before the closing boundary has left and before it,
    not set when the body breaks off earlier, one-shot.

Not covered by automated tests: the pickers, the viewer, other apps opening
files, decoding real photos (EXIF rotation), checked by hand on a device.

## Not in this stage

- Converting HEIC or compressing photos before upload; video.
- A foreground service for very long uploads.
- Drafts that survive the conversation being closed or the process killed.
- Deleting files on the server, which the API does not offer.

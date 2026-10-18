// Room attachments through the Gateway's Media API (docs/media/files.md).

import { ApiError } from './http.mjs';

export const MAX_FILE_BYTES = 10 * 1024 * 1024;
export const MAX_ATTACHMENTS = 5;

// fileProblem explains why a file cannot be uploaded, or returns null. The
// server decides on the content type; the client only checks the size, to
// avoid sending a file that is certain to be rejected.
export function fileProblem(file) {
  if (file.size === 0) return `«${file.name}»: файл пуст.`;
  if (file.size > MAX_FILE_BYTES) return `«${file.name}»: файл больше 10 МиБ.`;
  return null;
}

const sizeFormat = new Intl.NumberFormat('ru-RU', { minimumFractionDigits: 1, maximumFractionDigits: 1 });

export function formatSize(bytes) {
  if (bytes < 1024) return `${bytes} Б`;
  if (bytes < 1024 * 1024) return `${sizeFormat.format(bytes / 1024)} КиБ`;
  return `${sizeFormat.format(bytes / (1024 * 1024))} МиБ`;
}

// uploadFile stores a file in a room. Uploads have no idempotency key, so a
// failed upload is never retried automatically: the user decides.
export function uploadFile(api, roomId, file) {
  const form = new FormData();
  form.append('file', file, file.name);
  return api.call('POST', `/api/v1/rooms/${encodeURIComponent(roomId)}/files`, { body: form });
}

// downloadFile fetches a file's content. Downloads need the access token, so
// the content cannot be a plain link.
export function downloadFile(api, fileId) {
  return api.blob(`/api/v1/files/${encodeURIComponent(fileId)}/content`);
}

// FileInfos caches file metadata, which never changes. Every request checks
// current membership, so a file of a room the user was removed from
// resolves as {file_id, unavailable: true}.
export class FileInfos {
  #api;
  #known = new Map();
  #loading = new Map();

  constructor(api) {
    this.#api = api;
  }

  info(fileId) {
    return this.#known.get(fileId) ?? null;
  }

  get(fileId) {
    if (this.#known.has(fileId)) return Promise.resolve(this.#known.get(fileId));
    if (!this.#loading.has(fileId)) {
      const loading = this.#load(fileId).finally(() => this.#loading.delete(fileId));
      this.#loading.set(fileId, loading);
    }
    return this.#loading.get(fileId);
  }

  getAll(fileIds) {
    return Promise.all([...new Set(fileIds)].map((id) => this.get(id)));
  }

  // remember caches metadata returned by an upload.
  remember(info) {
    this.#known.set(info.file_id, info);
  }

  async #load(fileId) {
    let info;
    try {
      info = await this.#api.call('GET', `/api/v1/files/${encodeURIComponent(fileId)}`);
    } catch (err) {
      if (!(err instanceof ApiError) || err.status !== 404) throw err;
      info = { file_id: fileId, unavailable: true };
    }
    this.#known.set(fileId, info);
    return info;
  }
}

// Attachments are the uploaded files waiting to be sent with a message.
export class Attachments {
  #items = [];

  get items() {
    return [...this.#items];
  }

  get ids() {
    return this.#items.map((item) => item.file_id);
  }

  get full() {
    return this.#items.length >= MAX_ATTACHMENTS;
  }

  // add returns false when the file is already attached or the limit is reached.
  add(info) {
    if (this.full || this.#items.some((item) => item.file_id === info.file_id)) return false;
    this.#items.push(info);
    return true;
  }

  remove(fileId) {
    this.#items = this.#items.filter((item) => item.file_id !== fileId);
  }

  clear() {
    this.#items = [];
  }
}

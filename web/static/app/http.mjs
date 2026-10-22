// HTTP access to the Gateway's JSON API.

// ApiError is a failed request. status is the HTTP status, or 0 when the
// Gateway could not be reached; code is the Gateway's machine-readable code.
export class ApiError extends Error {
  constructor(status, code, message) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
  }
}

// requestJSON sends a request with an optional body and bearer token and
// returns the parsed JSON response, or null for 204 No Content. A FormData
// body is sent as multipart; anything else as JSON. Errors in the Gateway's
// envelope {"error":{"code","message"}} become ApiError.
export async function requestJSON(fetchFn, method, path, { body, token } = {}) {
  return readResponse(await send(fetchFn, method, path, { body, token }));
}

// requestBlob downloads a response body as a Blob, for file contents.
export async function requestBlob(fetchFn, path, { token } = {}) {
  const response = await send(fetchFn, 'GET', path, { token });
  if (!response.ok) await readResponse(response); // throws ApiError
  return response.blob();
}

async function send(fetchFn, method, path, { body, token }) {
  const headers = {};
  let payload;
  if (body instanceof FormData) {
    // fetch sets multipart/form-data with the boundary itself.
    payload = body;
  } else if (body !== undefined) {
    headers['Content-Type'] = 'application/json';
    payload = JSON.stringify(body);
  }
  if (token) headers.Authorization = `Bearer ${token}`;

  try {
    return await fetchFn(path, { method, headers, body: payload });
  } catch {
    throw new ApiError(0, 'network', 'Нет связи с сервером');
  }
}

async function readResponse(response) {
  if (response.status === 204) return null;

  const text = await response.text();
  let data = null;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = null;
    }
  }
  if (!response.ok) {
    const error = data && typeof data.error === 'object' ? data.error : null;
    throw new ApiError(
      response.status,
      error?.code ?? 'http_error',
      error?.message ?? `Ошибка сервера (HTTP ${response.status})`,
    );
  }
  return data;
}

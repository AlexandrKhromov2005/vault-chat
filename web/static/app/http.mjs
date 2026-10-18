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

// requestJSON sends a request with an optional JSON body and bearer token and
// returns the parsed JSON response, or null for 204 No Content. Errors in the
// Gateway's envelope {"error":{"code","message"}} become ApiError.
export async function requestJSON(fetchFn, method, path, { body, token } = {}) {
  const headers = {};
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  if (token) headers.Authorization = `Bearer ${token}`;

  let response;
  try {
    response = await fetchFn(path, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch {
    throw new ApiError(0, 'network', 'Нет связи с сервером');
  }
  return readResponse(response);
}

// readResponse parses a fetch response the way requestJSON does; it is shared
// with requests that are not JSON, such as file uploads.
export async function readResponse(response) {
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

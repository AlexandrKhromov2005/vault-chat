import { test } from 'node:test';
import assert from 'node:assert/strict';

import { describeError } from '../static/app/errors.mjs';
import { ApiError } from '../static/app/http.mjs';

const err = (status, code, message) => new ApiError(status, code, message);

test('known codes get a message for the user', () => {
  assert.match(describeError(err(0, 'network', '')), /Нет связи/);
  assert.match(describeError(err(429, 'rate_limited', 'too many requests')), /Слишком много запросов/);
  assert.match(describeError(err(404, 'not_found', 'room not found')), /не найден|нет доступа/i);
  assert.match(describeError(err(403, 'permission_denied', '')), /Недостаточно прав/);
  assert.match(describeError(err(409, 'failed_precondition', '')), /Владелец/);
  assert.match(describeError(err(503, 'unavailable', '')), /временно недоступен/);
});

test('duplicate accounts are told apart', () => {
  assert.equal(describeError(err(409, 'already_exists', 'username already taken')), 'Это имя пользователя уже занято.');
  assert.equal(describeError(err(409, 'already_exists', 'email already registered')), 'Этот email уже зарегистрирован.');
});

test('invalid input keeps the server explanation', () => {
  assert.equal(describeError(err(400, 'invalid_argument', 'invalid username')), 'Проверьте введённые данные: invalid username.');
});

test('anything else falls back to the server message', () => {
  assert.equal(describeError(err(500, 'internal', 'internal error')), 'internal error');
  assert.equal(describeError(new Error('')), 'Что-то пошло не так.');
});

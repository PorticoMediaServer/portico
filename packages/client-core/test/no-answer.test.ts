import test from 'node:test';
import assert from 'node:assert/strict';
import {serverDidNotAnswer} from '../src/no-answer.ts';
import {RequestTimeoutError} from '../src/http.ts';

const status = (code: number) => Object.assign(new Error('refused'), {status: code});

test('no answer at all: the network failed, on the web, on phones and in Safari', () => {
  for (const message of ['Failed to fetch', 'Network request failed', 'Load failed', 'fetch failed']) assert.equal(serverDidNotAnswer(new TypeError(message)), true, message);
  // A programming slip is a TypeError too, and says nothing about the server.
  assert.equal(serverDidNotAnswer(new TypeError('Cannot read properties of undefined')), false);
});

test('no answer at all: the server never answered in time', () => {
  assert.equal(serverDidNotAnswer(new RequestTimeoutError()), true);
});

test('no answer at all: a proxy or the server says it cannot serve now (502, 503, 504)', () => {
  for (const code of [502, 503, 504]) assert.equal(serverDidNotAnswer(status(code)), true, String(code));
});

test('an answer: a 4xx, including a route this server does not serve yet (404, 405), proves the server is there', () => {
  for (const code of [400, 401, 403, 404, 405, 409, 410, 422, 429]) assert.equal(serverDidNotAnswer(status(code)), false, String(code));
});

test('an answer: a 500 is the server answering with its own failure', () => {
  assert.equal(serverDidNotAnswer(status(500)), false);
});

test('an error a screen wrapped around the one it got is judged by what it wrapped; one with nothing to judge is not no-answer', () => {
  assert.equal(serverDidNotAnswer(Object.assign(new Error('Could not load'), {cause: status(405)})), false);
  assert.equal(serverDidNotAnswer(Object.assign(new Error('Could not load'), {cause: new TypeError('Failed to fetch')})), true);
  assert.equal(serverDidNotAnswer(new Error('Your server sent something Portico could not read')), false);
});

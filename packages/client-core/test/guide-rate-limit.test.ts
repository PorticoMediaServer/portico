import test from 'node:test';
import assert from 'node:assert/strict';
import {GUIDE_RATE_LIMIT_ATTEMPTS, isRateLimited, parseRetryAfter, rateLimitDelay, retryAfterMs} from '../src/guide/rate-limit.ts';

test('isRateLimited matches only HTTP 429', () => {
  assert.equal(isRateLimited({status: 429}), true);
  assert.equal(isRateLimited({status: 500}), false);
  assert.equal(isRateLimited(new Error('x')), false);
  assert.equal(isRateLimited(undefined), false);
});

test('parseRetryAfter accepts seconds and HTTP-dates, clamped', () => {
  const now = Date.parse('2026-09-23T00:00:00Z');
  assert.equal(parseRetryAfter(0, now), 0);
  assert.equal(parseRetryAfter(2, now), 2000);
  assert.equal(parseRetryAfter('3', now), 3000);
  assert.equal(parseRetryAfter(3600, now), 30_000, 'clamped at 30 s');
  const future = new Date(now + 5000).toUTCString();
  assert.ok(parseRetryAfter(future, now)! >= 4900 && parseRetryAfter(future, now)! <= 5000, future);
  assert.equal(parseRetryAfter(new Date(now - 5000).toUTCString(), now), 0, 'past dates wait nothing');
  assert.equal(parseRetryAfter('not-a-date', now), undefined);
  assert.equal(parseRetryAfter(-1, now), undefined);
  assert.equal(parseRetryAfter(undefined, now), undefined);
});

test('retryAfterMs reads seconds, raw strings and headers', () => {
  const now = Date.now();
  assert.equal(retryAfterMs({status: 429, retryAfterSeconds: 2}, now), 2000);
  assert.equal(retryAfterMs({status: 429, retryAfter: '1'}, now), 1000);
  assert.equal(retryAfterMs({status: 429, headers: {'Retry-After': '1'}}, now), 1000);
  assert.equal(retryAfterMs({status: 429, headers: {'retry-after': '1'}}, now), 1000);
  assert.equal(retryAfterMs({status: 429}, now), undefined);
  assert.equal(retryAfterMs({status: 500, retryAfterSeconds: 1}, now), undefined, 'only 429 honours Retry-After');
});

test('rateLimitDelay honours Retry-After with jitter, else bounded backoff', () => {
  const direct = rateLimitDelay({status: 429, retryAfterSeconds: 2}, 1, () => 0);
  assert.equal(direct, 2000);
  const jittered = rateLimitDelay({status: 429, retryAfterSeconds: 2}, 1, () => 0.999);
  assert.ok(jittered >= 2000 && jittered <= 2250, String(jittered));
  const backoff1 = rateLimitDelay({status: 429}, 1, () => 0);
  assert.equal(backoff1, 500);
  const backoff3 = rateLimitDelay({status: 429}, 3, () => 0);
  assert.equal(backoff3, 2000);
  const capped = rateLimitDelay({status: 429}, 9, () => 0);
  assert.equal(capped, 10_000);
  assert.equal(GUIDE_RATE_LIMIT_ATTEMPTS, 3);
});

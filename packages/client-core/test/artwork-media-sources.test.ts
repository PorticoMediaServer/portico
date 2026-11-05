import assert from 'node:assert/strict';
import test from 'node:test';
import {readAuthenticatedArtworkBytes} from '../src/artwork/media-sources.ts';

const identity = {serverId: 'server', viewerId: 'profile', authorityKey: 'original'};
const foreground = {kind: 'foreground' as const, url: '/v1/admin/access/members/member/profiles/profile/avatar?v=2&size=160'};
const ambient = {kind: 'ambient' as const, url: '/v1/items/title/art/backdrop?size=ambient-v1&v=' + 'a'.repeat(64)};
const status = (value: number) => (error: unknown) => (error as {status?: number})?.status === value;
const options = (fetcher: typeof fetch) => ({identity, isCurrent: () => true, currentToken: () => 'original-token',
  auth: {token: async (value: string) => value, recover: async () => undefined},
  api: {baseUrl: 'https://original.test', routeFetch: fetcher}});

test('foreign capabilities and retired authorities never consume credentials or send an image request', async () => {
  const input = options(async () => {assert.fail('unadmitted request');});
  input.auth.token = async () => {assert.fail('unadmitted credential read');};
  await assert.rejects(readAuthenticatedArtworkBytes(input, {...foreground, url: 'https://other.test/image'}, new AbortController().signal), status(403));
  await assert.rejects(readAuthenticatedArtworkBytes({...input, isCurrent: () => false}, foreground, new AbortController().signal), status(403));
});

test('ambient byte overflow and declared foreground overflow cancel the body at the original ceilings', async () => {
  for (const [request, declared, length] of [[ambient, undefined, 20001], [foreground, '12582913', 1]] as const) {
    let cancelled = false;
    const body = new ReadableStream<Uint8Array>({start(controller) {controller.enqueue(new Uint8Array(length));}, cancel() {cancelled = true;}});
    const headers: Record<string, string> = {'Content-Type': 'image/png'};
    if (declared) headers['Content-Length'] = declared;
    const input = options(async () => new Response(body, {headers}));
    await assert.rejects(readAuthenticatedArtworkBytes(input, request, new AbortController().signal), status(413));
    assert.equal(cancelled, true);
  }
});

test('unsupported image content is refused and cannot reach the platform decoder', async () => {
  const input = options(async () => new Response('<svg/>', {headers: {'Content-Type': 'image/svg+xml'}}));
  await assert.rejects(readAuthenticatedArtworkBytes(input, foreground, new AbortController().signal), status(415));
});

test('server retry guidance is reported without an image resend', async () => {
  let calls = 0;
  const input = options(async () => {calls++;return new Response(null, {status: 429, headers: {'Retry-After': '7'}});});
  await assert.rejects(readAuthenticatedArtworkBytes(input, foreground, new AbortController().signal),
    (error: unknown) => (error as {status?: number; retryAfterSeconds?: number}).status === 429 && (error as {retryAfterSeconds?: number}).retryAfterSeconds === 7);
  assert.equal(calls, 1);
});

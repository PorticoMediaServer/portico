import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readEventStream} from '../src/sse.ts';

const stream = (chunks: string[]) =>
  new Response(
    new ReadableStream({
      start(c) {
        const e = new TextEncoder();
        for (const x of chunks) c.enqueue(e.encode(x));
        c.close();
      },
    }),
  );

test('retry lines are accepted and ignored; ids with NUL stay put', async () => {
  const seen: {event: string; data: string; id: string}[] = [];
  await readEventStream(stream(['retry: 3000\ndata: hi\nid: 9\n\n']), (e) => seen.push(e));
  assert.deepEqual(seen, [{event: 'message', data: 'hi', id: '9'}]);
  const before: typeof seen = [];
  await readEventStream(stream(['id: 9\ndata: a\n\n', 'id: bad\0id\ndata: b\n\n']), (e) => before.push(e));
  assert.equal(before[1].id, '9', 'a NUL id does not move the resume point');
});

test('CR and CRLF endings frame like LF', async () => {
  const seen: string[] = [];
  await readEventStream(stream(['data: one\rdata: two\r\r', 'event: done\rdata: x\r\n\r\n']), (e) => seen.push(e.event + ':' + e.data));
  assert.deepEqual(seen, ['message:one\ntwo', 'done:x']);
});

test('a React Native text transport reads without a decoder', async () => {
  const seen: string[] = [];
  let done = false;
  const fake = {
    ok: true,
    status: 200,
    body: {
      getReader() {
        return {
          read: async () => {
            if (done) return {done: true, value: undefined};
            done = true;
            return {done: false, value: 'data: hello\n\n'};
          },
          cancel: async () => {},
          releaseLock: () => {},
        };
      },
    },
  };
  await readEventStream(fake as unknown as Response, (e) => seen.push(e.data));
  assert.deepEqual(seen, ['hello']);
});

test('an empty stream ends quietly; a field-less blank line emits nothing', async () => {
  const seen: string[] = [];
  await readEventStream(stream(['\n\n', ': comment\n\n']), (e) => seen.push(e.data));
  assert.deepEqual(seen, []);
});

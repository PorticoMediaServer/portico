import assert from 'node:assert/strict';
import test from 'node:test';
import {standardHomeApp} from './helpers/standard-home-app-harness.mjs';

// E-mobile-01 cold launch: Home was read about four times while it loaded. A cold start reads the
// Home document once (the restore's own read); only a real change reads it again.
test('a cold start with a healthy server reads the Home document exactly once', async () => {
  const reads: string[] = [], lines: string[] = [];
  const warn = console.warn;
  console.warn = (...args: unknown[]) => {if (typeof args[0] === 'string' && args[0].startsWith('portico_home_read ')) lines.push(args[0]); else warn(...args);};
  let app: Awaited<ReturnType<typeof standardHomeApp>> | undefined;
  try {
    app = await standardHomeApp({nativeResponse: async (request: any) => {
      if (request.method === 'GET' && /^\/v1\/home(?:\?|$)/.test(request.path)) reads.push(request.path);
      return undefined;
    }});
    // Home is on screen and its rows hydrate; nothing else reads the document again.
    await new Promise(resolve => setTimeout(resolve, 3000));
    assert.equal(reads.length, 1, reads.join(', '));
    assert.deepEqual(lines.map(line => JSON.parse(line.slice('portico_home_read '.length)).reason), ['restore']);
  } finally {
    console.warn = warn;
    if (app) {try {await app.unmount();} finally {app.disposeLoader();}}
  }
});

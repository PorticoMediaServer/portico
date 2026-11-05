import test from 'node:test';
import assert from 'node:assert/strict';
import {readConnectionFacts} from '../src/query/connection-operations.ts';
import {routePath} from '../../contracts/src/routes.ts';

test('anonymous connection facts use the platform executor without credentials or redirects', async () => {
  const signal = new AbortController().signal;
  let request: unknown[] = [];
  const fetcher: typeof fetch = async (url,options) => {request=[url,options];return new Response('{"remote":false}',{headers:{'Content-Type':'application/json'}});};
  assert.deepEqual(await readConnectionFacts('https://server.test',signal,fetcher),{remote:false});
  assert.deepEqual(request,['https://server.test'+routePath('get_connection'),{method:'GET',redirect:'error',credentials:'omit',referrerPolicy:'no-referrer',signal,headers:{Accept:'application/json'}}]);
  await assert.rejects(readConnectionFacts('https://server.test',signal,async()=>new Response('x'.repeat(8193))),/./);
});

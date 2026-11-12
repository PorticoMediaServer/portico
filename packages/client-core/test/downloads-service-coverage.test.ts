import {test} from 'node:test';
import assert from 'node:assert/strict';
import {DownloadsService} from '../src/downloads-service.ts';
import {downloaded, menu} from './helpers/downloads-next.ts';

test('a mismatched menu and a prepared file without its server hash fail plainly', async t => {
  const service = new DownloadsService({async request<T>(path: string): Promise<T> {return (path.includes('download-options') ? {...menu, itemId: 'other'} : {...downloaded, quality: '720p-low'}) as T;}});
  t.after(() => service.dispose());
  await assert.rejects(service.options('itm_film')); await assert.rejects(service.grant({id: '6_dl_oneitm_film'}));
});

test('a cancelled service aborts a shared refresh and an exact lookup with its own caller signal', async () => {
  const signals: AbortSignal[] = [];
  const service = new DownloadsService({request: <T>(_path: string, _method?: string, _body?: unknown, signal?: AbortSignal): Promise<T> => {
    signals.push(signal!); return new Promise((_yes, no) => signal!.addEventListener('abort', () => no(Object.assign(new Error('cancelled'), {name: 'AbortError'})), {once: true}));
  }});
  const first = service.refresh(); assert.equal(service.refreshRequests(), first);
  const lookup = service.lookupPreparations(['6_dl_oneitm_film'], new AbortController().signal);
  service.dispose(); await first; await assert.rejects(lookup, {name: 'AbortError'});
  assert.ok(signals.every(s => s.aborted)); assert.notEqual(service.getSnapshot().phase, 'error');
});

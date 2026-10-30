import test from 'node:test';
import assert from 'node:assert/strict';
import {SubtitleService} from '../src/subtitles.ts';
import {HttpLocalApi} from '../src/index.ts';

// IM04: a subtitle file is sent as multipart — bounded JSON metadata, then the raw file — under
// the same operation, revision and retry rules as the JSON save.
const catalog={version:1,itemId:'item',revision:1,canShare:false,sources:[{id:'source',durationUs:'100000000',inventoryRevision:1,available:true,timingKnown:true}],resources:[],discovered:[],provider:{id:'opensubtitles',enabled:false,reason:'provider_not_configured'}};
const settle=async()=>{for(let i=0;i<30;i++)await Promise.resolve();};
const fields={format:'sup' as const,language:'en',title:'English',rights:'Own transcript',scope:'personal' as const,offsetUs:'0'};

test('upload sends the metadata part first and the file as its own part', async () => {
  const sent: {path: string; method: string; body: FormData}[] = [];
  const api = {request: async <T,>() => catalog as T, async requestSubtitleUpload<T>(path: string, method: 'POST' | 'PUT', body: FormData) {
    sent.push({path, method, body}); return {operationId: 'op', resourceId: 'sub', revision: 1, catalogRevision: 2} as T;
  }};
  const service = new SubtitleService(api, {itemId: 'item', sourceId: 'source'}, () => 'op');
  service.start(); await settle();
  assert.equal(service.canUpload, true);
  const file = new Blob([new Uint8Array([0x50, 0x47, 1, 2])]);
  await service.upload({...fields, file, fileName: 'english.sup'});
  assert.equal(sent.length, 1);
  assert.equal(sent[0]!.path, '/v1/items/item/subtitles');
  assert.equal(sent[0]!.method, 'POST');
  const names = [...sent[0]!.body.keys()];
  assert.deepEqual(names, ['metadata', 'file']);
  const metadata = JSON.parse(sent[0]!.body.get('metadata') as string);
  assert.deepEqual(metadata, {...fields, operationId: 'op', sourceId: 'source', expectedRevision: 0});
  assert.equal(((sent[0]!.body.get('file')) as File).name, 'english.sup');
  // A replacement names the resource and its revision.
  await service.upload({...fields, file, fileName: 'english.sup'}, {id: 'sub', sourceId: 'source', revision: 3} as any);
  assert.equal(sent[1]!.path, '/v1/items/item/subtitles/sub');
  assert.equal(sent[1]!.method, 'PUT');
  assert.equal(JSON.parse(sent[1]!.body.get('metadata') as string).expectedRevision, 3);
  // Only a VobSub pair carries a companion.
  await assert.rejects(service.upload({...fields, file, fileName: 'a.sup', companion: file}));
  service.stop();
});

test('a service without the upload port refuses rather than guessing a transport', async () => {
  const service = new SubtitleService({request: async <T,>() => catalog as T}, {itemId: 'item', sourceId: 'source'}, () => 'op');
  assert.equal(service.canUpload, false);
  await assert.rejects(service.upload({...fields, file: new Blob(['x']), fileName: 'a.sup'}));
});

test('the HTTP upload names only a subtitle route and sends no JSON header', async () => {
  const calls: {url: string; init: RequestInit}[] = [];
  const api = new HttpLocalApi('https://server.example', 'token', (async (url: string, init: RequestInit) => { calls.push({url, init}); return new Response(JSON.stringify({ok: true}), {status: 200}); }) as typeof fetch);
  await assert.rejects(api.requestSubtitleUpload('/v1/items/a/art', 'POST', new FormData()));
  await assert.rejects(api.requestSubtitleUpload('/v1/items/a/subtitles', 'PUT', new FormData()));
  await api.requestSubtitleUpload('/v1/items/a/subtitles/r', 'PUT', new FormData());
  assert.equal(calls.length, 1);
  assert.equal(calls[0]!.url, 'https://server.example/v1/items/a/subtitles/r');
  const headers = calls[0]!.init.headers as Record<string, string>;
  assert.equal(headers['Content-Type'], undefined, 'the platform sets the multipart boundary');
  assert.equal(headers.Authorization, 'Bearer token');
});

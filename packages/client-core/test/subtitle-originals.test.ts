import test from 'node:test';
import assert from 'node:assert/strict';
import {drawnRenderingFor, readSubtitleFile, readSubtitleFontList, selectedSubtitleOriginal, subtitleOriginals} from '../src/subtitle-originals.ts';

const root = '/v1/media/subtitles/grant_1/res-1/3';
const styled = {id: 'res-1', revision: 3, format: 'ass', deliveries: [{format: 'text', url: root + '.vtt'}, {format: 'ass', url: root + '/original.ass', fontsUrl: root + '/fonts'}]};
const dvd = {id: 'res-1', revision: 3, format: 'vobsub', deliveries: [{format: 'vobsub', url: root + '/original.idx', companionUrl: root + '/original.sub'}]};

test('what a track offers: only originals this code understands, at addresses under the media grant', () => {
  const cases: {name: string; resource: unknown; want: unknown[]}[] = [
    {name: 'styled, with its fonts', resource: styled, want: [{format: 'ass', url: root + '/original.ass', fontsUrl: root + '/fonts'}]},
    {name: 'Blu-ray', resource: {deliveries: [{format: 'pgs', url: root + '/original.sup'}]}, want: [{format: 'pgs', url: root + '/original.sup'}]},
    {name: 'DVD, two files', resource: dvd, want: [{format: 'vobsub', url: root + '/original.idx', companionUrl: root + '/original.sub'}]},
    {name: 'DVD without its second file', resource: {deliveries: [{format: 'vobsub', url: root + '/original.idx'}]}, want: []},
    {name: 'DVB', resource: {deliveries: [{format: 'dvb', url: root + '/original.mks'}]}, want: [{format: 'dvb', url: root + '/original.mks'}]},
    {name: 'text only', resource: {deliveries: [{format: 'text', url: root + '.vtt'}]}, want: []},
    {name: 'no list (an older server)', resource: {id: 'res-1', revision: 3, format: 'ass'}, want: []},
    {name: 'a format from a newer server', resource: {deliveries: [{format: 'ttml-image', url: root + '/original.sup'}]}, want: []},
    {name: 'an address somewhere else', resource: {deliveries: [{format: 'pgs', url: 'https://elsewhere.example/original.sup'}, {format: 'pgs', url: '/v1/items/x/original.sup'}]}, want: []},
    {name: 'a live stream address cannot stand in for a subtitle original', resource: {deliveries: [{format: 'ass', url: '/v1/media/linear/subtitles/a/b/original.ass'}]}, want: []},
    {name: 'the wrong file for the format', resource: {deliveries: [{format: 'pgs', url: root + '/original.ass'}]}, want: []},
    {name: 'fonts from another track are dropped, the script is kept', resource: {deliveries: [{format: 'ass', url: root + '/original.ssa', fontsUrl: '/v1/media/subtitles/grant_1/other/3/fonts'}]}, want: [{format: 'ass', url: root + '/original.ssa'}]},
    {name: 'not a resource', resource: null, want: []},
    {name: 'a list of rubbish', resource: {deliveries: [1, null, 'x', {format: 'ass'}, {url: root + '/original.ass'}]}, want: []},
  ];
  for (const c of cases) assert.deepEqual(subtitleOriginals(c.resource), c.want, c.name);
});

test('what to ask for depends on what the device draws', () => {
  assert.equal(drawnRenderingFor(styled, ['text', 'ass', 'pgs']), 'ass');
  assert.equal(drawnRenderingFor(styled, ['text']), undefined, 'a device with no styled renderer takes the text');
  assert.equal(drawnRenderingFor(dvd, ['text', 'ass', 'pgs']), undefined);
  assert.equal(drawnRenderingFor(dvd, ['vobsub']), 'vobsub');
  assert.equal(drawnRenderingFor({format: 'srt', deliveries: [{format: 'text', url: root + '.vtt'}]}, ['text', 'ass', 'pgs', 'vobsub', 'dvb']), undefined);
});

test('the plan says when the device is to draw', () => {
  assert.deepEqual(selectedSubtitleOriginal({renderer: 'client', mode: 'track', selected: styled}), {format: 'ass', url: root + '/original.ass', fontsUrl: root + '/fonts'});
  assert.equal(selectedSubtitleOriginal({renderer: 'external_text', mode: 'track', selected: styled}), null, 'text delivery of the same track');
  assert.equal(selectedSubtitleOriginal({renderer: 'burn_in', mode: 'track', selected: dvd}), null);
  assert.equal(selectedSubtitleOriginal({renderer: 'client', mode: 'off', selected: null}), null);
  assert.equal(selectedSubtitleOriginal(null), null);
  for (const selected of [{...styled, id: 'other'}, {...styled, revision: 4}, {...styled, revision: undefined}]) {
    assert.equal(selectedSubtitleOriginal({renderer: 'client', mode: 'track', selected}), null, 'the selected resource and revision own the addresses');
  }
});

const response = (body: Uint8Array | string, init: {status?: number; headers?: Record<string, string>; chunk?: number} = {}) => {
  const bytes = typeof body === 'string' ? new TextEncoder().encode(body) : body, step = init.chunk ?? 7;
  let at = 0, cancelled = false;
  const stream = new ReadableStream<Uint8Array>({pull(controller) { if (at >= bytes.length) { controller.close(); return; } controller.enqueue(bytes.slice(at, at + step)); at += step; }, cancel() { cancelled = true; }});
  return Object.assign(new Response(init.status && init.status >= 400 ? null : stream, {status: init.status ?? 200, headers: init.headers}), {wasCancelled: () => cancelled});
};
const fetching = (answer: Response, seen: {url?: string; init?: RequestInit} = {}) => ((url: string, init?: RequestInit) => { seen.url = url; seen.init = init; return Promise.resolve(answer); }) as unknown as typeof fetch;

test('a file is read whole, without cookies or the cache, and never past its bound', async () => {
  const seen: {url?: string; init?: RequestInit} = {};
  const file = await readSubtitleFile(fetching(response('[Script Info]\nTitle: x', {headers: {'Content-Type': 'text/x-ssa; charset=windows-1251'}}), seen), 'https://server.example' + root + '/original.ass', 1024, new AbortController().signal);
  assert.equal(new TextDecoder().decode(file.bytes), '[Script Info]\nTitle: x');
  assert.equal(file.charset, 'windows-1251');
  assert.deepEqual([seen.init?.credentials, seen.init?.cache, seen.init?.redirect], ['omit', 'no-store', 'error']);
  assert.equal((await readSubtitleFile(fetching(response(new Uint8Array(40), {headers: {'Content-Type': 'application/octet-stream'}})), 'u', 40, new AbortController().signal)).charset, '');
  await assert.rejects(readSubtitleFile(fetching(response(new Uint8Array(41))), 'u', 40, new AbortController().signal), {code: 'invalid_subtitles'}, 'a body longer than the bound');
  await assert.rejects(readSubtitleFile(fetching(response(new Uint8Array(4), {headers: {'Content-Length': '99999999'}})), 'u', 40, new AbortController().signal), {code: 'invalid_subtitles'}, 'a declared length over the bound is refused before reading');
  for (const status of [401, 403, 404, 410, 500]) await assert.rejects(readSubtitleFile(fetching(response('', {status})), 'u', 40, new AbortController().signal), {status});
  const stop = new AbortController();
  stop.abort();
  await assert.rejects(readSubtitleFile(fetching(response(new Uint8Array(4))), 'u', 40, stop.signal));
});

test('the font list: numbered fonts under the same track only', async () => {
  const list = JSON.stringify({fonts: [{name: 'Sign Font.ttf', url: root + '/fonts/0'}, {name: 'x', url: root + '/fonts/../../secret'}, {name: 'y', url: 'https://elsewhere.example/font'}, {url: root + '/fonts/12'}, 5], skippedCount: 1});
  const fonts = await readSubtitleFontList(fetching(response(list, {headers: {'Content-Type': 'application/json'}})), 'https://server.example' + root + '/fonts', root + '/fonts', new AbortController().signal);
  assert.deepEqual(fonts, [{name: 'Sign Font.ttf', url: root + '/fonts/0'}, {name: '', url: root + '/fonts/12'}]);
  await assert.rejects(readSubtitleFontList(fetching(response('not json')), 'u', root + '/fonts', new AbortController().signal), {code: 'invalid_subtitles'});
  await assert.rejects(readSubtitleFontList(fetching(response('{"fonts":{}}')), 'u', root + '/fonts', new AbortController().signal), {code: 'invalid_subtitles'});
  await assert.rejects(readSubtitleFontList(fetching(response(list)), 'u', '/v1/items/x/fonts', new AbortController().signal), {code: 'invalid_subtitles'});
  const many = JSON.stringify({fonts: Array.from({length: 80}, (_, i) => ({name: 'f', url: root + '/fonts/' + i}))});
  assert.equal((await readSubtitleFontList(fetching(response(many)), 'u', root + '/fonts', new AbortController().signal)).length, 32);
});

test('an unselected manifest advertises rendering before a media grant is pinned', () => {
  for (const format of ['ass', 'pgs', 'vobsub', 'dvb'] as const) {
    const resource = {deliveries: [{format}]};
    assert.equal(drawnRenderingFor(resource, ['text', format]), format);
    assert.equal(drawnRenderingFor(resource, ['text']), undefined);
    assert.deepEqual(subtitleOriginals(resource), [], 'a rendering offer grants no file access');
    assert.equal(selectedSubtitleOriginal({renderer: 'client', mode: 'track', selected: resource}), null, 'a selected plan still needs valid media addresses');
  }
  for (const entry of [{format: 'unknown'}, {format: 'pgs', url: ''}, {format: 'pgs', url: 'https://elsewhere.example/sub.sup'}, {format: 'vobsub', companionUrl: '/elsewhere'}, {format: 'ass', fontsUrl: '/elsewhere'}]) {
    assert.equal(drawnRenderingFor({deliveries: [entry]}, ['ass', 'pgs', 'vobsub', 'dvb']), undefined);
  }
});

test('an image original names where its forced-line count is read, and Forced only is offered only for a mix', async () => {
  const {forcedChoiceOf, parseSubtitleLines} = await import('../src/subtitle-originals.ts');
  const named = subtitleOriginals({deliveries: [{format: 'pgs', url: root + '/original.sup', linesUrl: root + '/lines'}]});
  assert.deepEqual(named, [{format: 'pgs', url: root + '/original.sup', linesUrl: root + '/lines'}]);
  // An address for another track is not taken.
  assert.deepEqual(subtitleOriginals({deliveries: [{format: 'pgs', url: root + '/original.sup', linesUrl: '/v1/media/subtitles/grant_1/other/3/lines'}]}), [{format: 'pgs', url: root + '/original.sup'}]);
  assert.equal(forcedChoiceOf(parseSubtitleLines({forced: 12, total: 900})), true);
  assert.equal(forcedChoiceOf(parseSubtitleLines({forced: 0, total: 900})), false);
  assert.equal(forcedChoiceOf(parseSubtitleLines({forced: 40, total: 40})), false);
  assert.equal(parseSubtitleLines({forced: 5, total: 2}), undefined);
  assert.equal(forcedChoiceOf(undefined), false);
});


test('a retired read never starts fetch and cancellation while awaiting headers cancels the body', async () => {
  const retired = new AbortController(); retired.abort();
  let requests = 0;
  const never = (() => { requests++; return new Promise<Response>(() => {}); }) as typeof fetch;
  await assert.rejects(readSubtitleFile(never, 'u', 40, retired.signal));
  assert.equal(requests, 0);
  for (const limit of [0, -1, NaN, Infinity]) {
    await assert.rejects(readSubtitleFile(never, 'u', limit, new AbortController().signal), {code: 'invalid_subtitles'});
  }
  assert.equal(requests, 0);
  const pending = new AbortController();
  const body = response(new Uint8Array(4));
  const ignoredAbort = (() => { pending.abort(); return Promise.resolve(body); }) as typeof fetch;
  await assert.rejects(readSubtitleFile(ignoredAbort, 'u', 40, pending.signal));
  assert.equal(body.wasCancelled(), true);
});

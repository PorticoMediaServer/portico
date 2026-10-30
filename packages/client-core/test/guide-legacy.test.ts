import test from 'node:test';
import assert from 'node:assert/strict';
import {parseChannelGuide} from '../src/channel-guide.ts';
import {GuideWindowStore, HOUR_MS, legacyChannel, legacyChannelSources, legacyGuideSource, legacyGuideState, legacyProgram, legacyProgramme, layoutRow, pxPerMsFor, type GuideChannelRow} from '../src/guide/index.ts';
import {demoChannels, demoPrograms} from '../src/guide/testing/fixture.ts';
import {legacyChannelRow} from '../src/guide/legacy-source.ts';

const settle = async () => { for (let i = 0; i < 30; i++) await new Promise(setImmediate); };
const SERVER = 'srv';
const NOW = Date.parse('2026-09-22T19:20:00Z');

test('a captured channel search reaches directory, named programmes and neighbor reads', async () => {
  const {api, requests} = legacyApi(), stop = new AbortController();
  const source = legacyGuideSource(api, SERVER, {kind: 'live-source', sourceId: 'antenna', timezone: 'UTC', search: 'Summit'});
  const page = await source.channels(0, 1, stop.signal);
  await source.programs([page.items[0]!.id], NOW, NOW + HOUR_MS, stop.signal);
  await source.neighbor?.(page.items[0]!, 1, stop.signal);
  assert.ok(requests.some(path => path.startsWith('/v1/guide/channels?')));
  assert.ok(requests.some(path => path.startsWith('/v1/guide?')));
  assert.ok(requests.every(path => new URL(path, 'https://example.test').searchParams.get('search') === 'Summit'));
});

/** Current directory, source summaries and named programme windows over the demo fixture. */
function legacyApi(opts: {library?: boolean; failKind?: string; refreshState?: string} = {}) {
  const requests: string[] = [];
  const library = [{id: 'lc-1', name: 'Sharks', number: '900', group: ''}, {id: 'lc-2', name: 'Aardvarks', number: '901', group: ''}];
  const api = {
    async request<T>(path: string): Promise<T> {
      if (!path.startsWith('/v1/guide')) throw Object.assign(new Error('not found'),{status:404});
      requests.push(path);
      const q = new URL(path, 'http://x').searchParams;
      const sourceRows = (kind: string) => kind === 'live-source' ? demoChannels : opts.library ? library : [];
      const meta = (kind: string) => ({id: kind === 'live-source' ? 'antenna' : 'library-src', name: kind === 'live-source' ? 'Antenna' : 'Library Channels', generation: 'g1', publishedAt: new Date(NOW).toISOString(), availableStart: '', availableEnd: '', provenance: kind, refreshState: opts.refreshState ?? 'healthy'});
      const directoryRow = (c: {id:string;name:string;number:string;group:string}, kind:string) => ({...c,sourceId:meta(kind).id,provenance:kind,generation:'g1',logoPath:'',programmes:[],tuneAvailable:true,recordAvailable:kind==='live-source',tuneUnavailableReason:'',recordUnavailableReason:kind==='live-source'?'':'not-recordable',favorite:false,hidden:false,preferenceRevision:0});
      if (path.startsWith('/v1/guide/sources?')) {
        const kinds = ['live-source', ...(opts.library ? ['library-channel'] : [])];
        return {protocolVersion:'1.0',serverId:SERVER,viewerFence:'fence',revision:'r1',sources:kinds.map((kind,position)=>({...meta(kind),position,recordAvailable:kind==='live-source',guideDays:8,channelCount:sourceRows(kind).length,favoriteCount:0,groups:[],groupCounts:[],unavailableReasons:{}}))} as T;
      }
      if (path.startsWith('/v1/guide/channels?')) {
        const kind=q.get('kind')!, kinds=kind==='all'?['live-source','library-channel']:[kind];
        if (kind === opts.failKind) throw new Error('unavailable');
        const all=kinds.filter(k=>k!==opts.failKind).flatMap(k=>sourceRows(k).map(c=>directoryRow(c,k)));
        if(q.get('sort')==='name')all.sort((a,b)=>a.name.localeCompare(b.name));
        const offset=Number(q.get('offset')),limit=Number(q.get('limit')),channels=all.slice(offset,offset+limit);
        return {protocolVersion:'1.0',serverId:SERVER,directory:{viewerFence:'fence',revision:'r1',total:all.length,offset,limit,channels,sources:kinds.map(meta)}} as T;
      }
      const kind = q.get('kind')!, start = q.get('start')!, end = q.get('end')!;
      if (kind === opts.failKind) throw new Error('unavailable');
      const page = Number(q.get('cursor') || 0);
      const named = q.get('channels')?.split(',');
      const all = (kind === 'live-source' ? demoChannels : opts.library ? library : []).filter(c => !named || named.includes(c.id));
      const rowsOnly = q.get('programmes') === 'none';
      const slice = all.slice(page * 30, page * 30 + 30);
      const channels = slice.map(c => ({
        id: c.id, sourceId: kind === 'live-source' ? 'antenna' : 'library-src', provenance: kind, name: c.name, number: c.number, group: c.group, generation: 'g1', logoPath: '',
        programmes: (rowsOnly ? [] : demoPrograms(c.id, Date.parse(start), Date.parse(end))).map(p => ({id: p.id, channelId: c.id, title: p.title, start: new Date(p.start).toISOString(), end: new Date(p.end).toISOString(), lineage: 'provider-id', seriesId: p.seriesId ?? '', episodeId: '', newEvidence: p.flags?.new ? 'new' : 'unknown', description: `About ${p.title}`})),
        tuneAvailable: true, recordAvailable: kind === 'live-source', tuneUnavailableReason: '', recordUnavailableReason: kind === 'live-source' ? '' : 'not-recordable', favorite: false, hidden: false, preferenceRevision: 0,
      }));
      return {protocolVersion: '1.0', serverId: SERVER, guide: {
        state: all.length ? 'ready' : 'no-channels', viewerFence: 'fence', start, end, timezone: q.get('timezone'), observedAt: new Date(NOW).toISOString(),
        nextCursor: (page + 1) * 30 < all.length ? String(page + 1) : '', channels,
        sources: kind === 'live-source' ? [{id: 'antenna', name: 'Antenna', generation: 'g1', publishedAt: new Date(NOW).toISOString(), availableStart: '', availableEnd: '', provenance: kind, refreshState: opts.refreshState ?? 'healthy'}]
          : opts.library ? [{id: 'library-src', name: 'Library Channels', generation: 'g1', publishedAt: new Date(NOW).toISOString(), availableStart: '', availableEnd: '', provenance: kind, refreshState: 'healthy'}] : [],
      }} as T;
    },
  };
  return {api, requests};
}

test('guide adapter: the demo guide through the windowed store, one request per block', async () => {
  const {api, requests} = legacyApi();
  const source = legacyGuideSource(api, SERVER, {kind: 'live-source', sourceId: '', timezone: 'America/New_York', now: () => NOW});
  const store = new GuideWindowStore({source, channelPageSize: 50});
  const view = {start: NOW - 20 * 60_000, end: NOW + 2 * HOUR_MS};
  store.setViewport({firstRow: 0, lastRow: 13, ...view});
  await settle();
  assert.equal(store.getSnapshot().total, 14);
  assert.equal(store.channelAt(12)!.name, 'Quiet Channel (no guide)');
  const row = store.programsFor(0, view.start, view.end);
  assert.equal(row.complete, true);
  const cells = layoutRow(row.programs, {viewStart: view.start, viewEnd: view.end, pxPerMs: pxPerMsFor(240), now: NOW});
  assert.ok(cells.some(c => c.state === 'now'));
  assert.deepEqual(layoutRow(store.programsFor(12, view.start, view.end).programs, {viewStart: view.start, viewEnd: view.end, pxPerMs: pxPerMsFor(240), now: NOW}).map(c => c.kind), ['gap']);
  // The initial viewport requests its page and the next margin page while
  // the total is unknown, then only named programme windows.
  assert.equal(requests.filter(r => r.startsWith('/v1/guide/channels?')).length, 2);
  assert.ok(requests.filter(r => r.startsWith('/v1/guide?')).every(r => new URL(r, 'http://x').searchParams.has('channels')));
  assert.ok(requests.length <= 6, `${requests.length} requests`);
  store.dispose();
});

test('guide adapter: Channels entries from today\'s API', async () => {
  const {api} = legacyApi();
  const sources = await legacyChannelSources(api, SERVER, 'America/New_York', 'Library Channels', NOW);
  assert.deepEqual(sources.map(s => [s.id, s.name, s.type, s.recordAvailable]), [['antenna', 'Antenna', 'live', true]]);
});

test('guide adapter: rows and programs carry their /v1/guide objects and descriptions', async () => {
  const {api} = legacyApi();
  const source = legacyGuideSource(api, SERVER, {kind: 'live-source', sourceId: '', timezone: 'UTC', now: () => NOW});
  const signal = new AbortController().signal;
  const {items} = await source.channels(0, 3, signal);
  const native = legacyChannel(items[0]!);
  assert.equal(native?.id, items[0]!.id);
  assert.equal(native?.generation, 'g1');
  const programs = await source.programs([items[0]!.id], NOW, NOW + HOUR_MS, signal);
  const first = programs[items[0]!.id]![0]!;
  assert.equal(first.description, `About ${first.title}`);
  assert.equal(legacyProgramme(first)?.lineage, 'provider-id');
  // A look-alike object the adapter didn't make is never trusted.
  const forged: GuideChannelRow = {...items[0]!, native: {...native}};
  assert.equal(legacyChannel(forged), undefined);
  assert.equal(legacyProgramme({...first, native: {}}), undefined);
});

test('guide adapter preserves the authorized mark exactly and leaves absent marks absent', async () => {
  const {api} = legacyApi();
  const page = await legacyGuideSource(api, SERVER, {kind: 'live-source', sourceId: 'antenna', timezone: 'UTC'})
    .channels(0, 1, new AbortController().signal);
  const channel = legacyChannel(page.items[0]!)!;
  assert.equal(page.items[0]!.mark, undefined);
  const mark = {text: 'Q', seed: 'actual-source/channel-seed'};
  const marked = legacyChannelRow({...channel, mark});
  assert.deepEqual(marked.mark, mark);
  assert.equal(legacyChannel(marked)?.mark, mark);
});

test('guide adapter: All channels joins live sources and Library Channels, ordered, and survives one kind failing', async () => {
  const signal = new AbortController().signal;
  const {api, requests} = legacyApi({library: true});
  const all = legacyGuideSource(api, SERVER, {kind: 'all', sourceId: '', timezone: 'UTC', sort: 'number', includeHidden: true, now: () => NOW});
  const page = await all.channels(0, 50, signal);
  assert.equal(page.total, 16);
  assert.deepEqual(page.items.slice(-2).map(r => [r.name, r.kind]), [['Sharks', 'library'], ['Aardvarks', 'library']]);
  assert.ok(requests.every(r => r.includes('includeHidden=true')));
  const byName = await legacyGuideSource(api, SERVER, {kind: 'all', sourceId: '', timezone: 'UTC', sort: 'name', now: () => NOW}).channels(0, 50, signal);
  assert.equal(byName.items[0]!.name, 'Aardvarks');
  // Library Channels unavailable: the live guide still shows.
  const partial = await legacyGuideSource(legacyApi({library: true, failKind: 'library-channel'}).api, SERVER, {kind: 'all', sourceId: '', timezone: 'UTC', now: () => NOW}).channels(0, 50, signal);
  assert.equal(partial.total, 14);
  // Every kind failing is an error, not an empty guide.
  await assert.rejects(legacyGuideSource(legacyApi({failKind: 'live-source'}).api, SERVER, {kind: 'live-source', sourceId: '', timezone: 'UTC', now: () => NOW}).channels(0, 10, signal));
});

test('guide adapter: only degraded and credentials-required sources are out of date', async () => {
  assert.equal(legacyGuideState('degraded'), 'stale');
  assert.equal(legacyGuideState('credentials-required'), 'stale');
  for (const state of ['healthy', '', 'refreshed', 'something-new']) assert.equal(legacyGuideState(state), 'ready', state);
  const sources = await legacyChannelSources(legacyApi({refreshState: 'credentials-required'}).api, SERVER, 'UTC', 'Library Channels', NOW);
  assert.equal(sources[0]!.guideState, 'stale');
  const unknown = await legacyChannelSources(legacyApi({refreshState: 'warming-up'}).api, SERVER, 'UTC', 'Library Channels', NOW);
  assert.equal(unknown[0]!.guideState, 'ready');
});

test('guide adapter: programme facts map behind presence, malformed facts never fatal', async () => {
  const route = {kind: 'live-source' as const, start: new Date(NOW).toISOString(), end: new Date(NOW + HOUR_MS).toISOString(), timezone: 'UTC', search: '', sourceId: ''};
  const base = {id: 'p', channelId: 'c', title: 'Harbour Lights', start: route.start, end: route.end, lineage: 'provider-id'};
  const full = parseChannelGuide({protocolVersion: '1.0', serverId: SERVER, guide: {state: 'ready', viewerFence: 'f', start: route.start, end: route.end, timezone: 'UTC', nextCursor: '', observedAt: route.start, days: 8, sources: [{id: 's', name: 'Antenna', generation: 'g', publishedAt: route.start, availableStart: '', availableEnd: '', provenance: 'live-source', refreshState: 'healthy'}], channels: [{id: 'c', sourceId: 's', provenance: 'live-source', name: 'One', number: '1', group: '', generation: 'g', tuneAvailable: true, recordAvailable: true, tuneUnavailableReason: '', recordUnavailableReason: '', favorite: false, hidden: false, preferenceRevision: 0, programmes: [{...base, subtitle: 'The Arrival', episode: {season: 2, number: 5}, categories: ['Drama', 'Mystery'], rating: {system: 'VCHIP', value: 'TV-14'}, year: 2019, starRating: '7/10', flags: {live: true, new: false, premiere: false, repeat: false}, image: '/v1/items/item123/art/poster'}]}]}}, SERVER, route);
  const mapped = legacyProgram(full.channels[0].programmes[0]!);
  assert.equal(mapped.subtitle, 'The Arrival');
  assert.deepEqual(mapped.episode, {season: 2, number: 5});
  assert.deepEqual(mapped.categories, ['Drama', 'Mystery']);
  assert.equal(mapped.rating, 'TV-14');
  assert.equal(mapped.year, 2019);
  assert.equal(mapped.starRating, '7/10');
  assert.equal(mapped.flags?.live, true);
  assert.equal(mapped.image, '/v1/items/item123/art/poster');
  assert.equal(full.days, 8);
  const minimal = parseChannelGuide({protocolVersion: '1.0', serverId: SERVER, guide: {state: 'ready', viewerFence: 'f', start: route.start, end: route.end, timezone: 'UTC', nextCursor: '', observedAt: route.start, sources: [{id: 's', name: 'Antenna', generation: 'g', publishedAt: route.start, availableStart: '', availableEnd: '', provenance: 'live-source', refreshState: 'healthy'}], channels: [{id: 'c', sourceId: 's', provenance: 'live-source', name: 'One', number: '1', group: '', generation: 'g', tuneAvailable: true, recordAvailable: true, tuneUnavailableReason: '', recordUnavailableReason: '', favorite: false, hidden: false, preferenceRevision: 0, programmes: [{...base}]}]}}, SERVER, route);
  const bare = legacyProgram(minimal.channels[0].programmes[0]!);
  assert.equal(bare.subtitle, undefined);
  assert.equal(bare.rating, undefined);
  assert.equal(bare.starRating, undefined);
  assert.equal(bare.image, undefined);
  assert.equal(minimal.days, undefined);
  const malformed = parseChannelGuide({protocolVersion: '1.0', serverId: SERVER, guide: {state: 'ready', viewerFence: 'f', start: route.start, end: route.end, timezone: 'UTC', nextCursor: '', observedAt: route.start, days: 'many' as never, sources: [{id: 's', name: 'Antenna', generation: 'g', publishedAt: route.start, availableStart: '', availableEnd: '', provenance: 'live-source', refreshState: 'healthy'}], channels: [{id: 'c', sourceId: 's', provenance: 'live-source', name: 'One', number: '1', group: '', generation: 'g', tuneAvailable: true, recordAvailable: true, tuneUnavailableReason: '', recordUnavailableReason: '', favorite: false, hidden: false, preferenceRevision: 0, programmes: [{...base, subtitle: 42 as never, episode: {season: 0} as never, categories: 'Drama' as never, rating: {value: 42} as never, year: 'n/a' as never, image: 'https://provider.invalid/x.png'}]}]}}, SERVER, route);
  const dropped = legacyProgram(malformed.channels[0].programmes[0]!);
  assert.equal(dropped.subtitle, undefined);
  assert.equal(dropped.episode, undefined);
  assert.deepEqual(dropped.categories, []);
  assert.equal(dropped.rating, undefined);
  assert.equal(dropped.image, undefined);
  assert.equal(malformed.days, undefined);
});

test('guide adapter: channel sources carry summary guide days without programme reads', async () => {
  const {api, requests} = legacyApi();
  const sources = await legacyChannelSources(api, SERVER, 'UTC', 'Library Channels', NOW);
  assert.equal(sources[0]!.guideDays, 8);
  assert.equal(requests.length, 1);
  assert.ok(requests[0].startsWith('/v1/guide/sources?'));
});

test('the guide asks for the channels on screen, never the whole lineup per window', async () => {
  const {api, requests} = legacyApi();
  const source = legacyGuideSource(api, SERVER, {kind: 'live-source', sourceId: '', timezone: 'America/New_York', now: () => NOW});
  const signal = new AbortController().signal;
  const list = await source.channels(0, 3, signal);
  assert.equal(list.total, 14);
  assert.ok(requests.every(r => r.startsWith('/v1/guide/channels?')), 'the channel list uses the bounded directory');
  requests.length = 0;
  const ids = list.items.map(r => r.id);
  const programs = await source.programs(ids, NOW, NOW + 3 * HOUR_MS, signal);
  assert.deepEqual(Object.keys(programs).sort(), [...ids].sort());
  assert.ok(ids.some(id => programs[id]!.length > 0));
  assert.equal(requests.length, 1, 'one request for three visible channels');
  assert.deepEqual(new URL(requests[0]!, 'http://x').searchParams.get('channels')!.split(',').sort(), [...ids].sort());
});

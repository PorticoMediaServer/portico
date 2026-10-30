import {strict as assert} from 'node:assert';
import {test} from 'node:test';
import {createMusicFavorite, reportMusicFavoriteLoadFailure} from '../src/screens/watch/music/favorite-binding.ts';

function harness() {
  const authority = {epoch: 1, serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', viewerFence: 'viewer'};
  const fence = {kind: 'queue', playback: {scope: {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane'}, run: 1}, sessionId: 'session', generation: 1, sourceId: 'source', itemId: 'song', entryId: 'entry'};
  let current = true, favorite = false, revision = 4, failed = false;
  let snapshot: any = {authority, fence, disposed: false, playback: {playbackRun: 3}, presentation: {kind: 'music', expanded: true, fence}, dock: {local: {kind: 'music', expanded: true, fence}},
    queueView: {queue: {currentEntryId: 'entry'}, items: [{entryId: 'entry', itemId: 'song', libraryId: 'library', kind: 'song'}]}};
  let session: any = {viewer: authority, sessionFamilyId: 'family', deviceId: 'device'};
  let held: Promise<void> | undefined, release: (() => void) | undefined;
  const requests: any[] = [], acks:string[]=[];
  const personal = () => ({watchlisted: false, favorite, rating: null, revision, watched: false, progressMs: 0, lastPlayedAt: '', status: 'current', conflicts: []});
  const detail = () => ({scope: {serverId: 'server', libraryId: 'library', itemId: 'song', viewerFence: 'detail-viewer'}, revision: {catalog: 1, viewer: 1},
    item: {id: 'song', libraryId: 'library', kind: 'song', title: 'Real song', durationMs: 180000, progressMs: 0, available: true}, personal: personal(),
    actions: [{id: 'favorite', labelKey: 'entry.addToFavorites', enabled: true}], metadata: {status: 'unavailable', ratings: [], genres: [], credits: []}});
  const context: any = {epoch: 1, session: () => session, isCurrent: () => current, api: {baseUrl: 'https://local.invalid', getRouteConnection: () => undefined, acknowledgeUserMutation:async(id:string)=>{acks.push(id);},
    async request(path: string, method: string, body?: any) {
      requests.push({path, method, body});
      if (method === 'PUT') {
        if (failed) {failed = false;throw new TypeError('connection lost');}
        if (held) await held;
        favorite = body.favorite;revision++;
        return {operationId: body.operationId, serverId: 'server', libraryId: 'library', itemId: 'song', viewerFence: 'detail-viewer', current: personal()};
      }
      return detail();
    }}};
  const runtime: any = {getSnapshot: () => snapshot};
  const owner = () => createMusicFavorite({context, runtime, authority: snapshot.authority, fence: snapshot.fence, run: snapshot.playback.playbackRun, itemId: 'song', libraryId: 'library', isCurrent: () => current});
  return {owner, requests, acks, get snapshot() {return snapshot;}, retire() {current = false;}, replace() {snapshot = {...snapshot, fence: {...snapshot.fence, sourceId: 'replacement'}};},
    changeRun() {snapshot = {...snapshot, playback: {playbackRun: 4}};}, changeViewer() {session = {...session, viewer: {...session.viewer, profileId: 'other'}};},
    failNext() {failed = true;}, hold() {held = new Promise<void>(resolve => {release = resolve;});return () => release!();}};
}

test('music Favorite reads and mutates the canonical personal revision for the captured song', async () => {
  const f = harness(), owner = f.owner();
  try {
    await owner.load();const shown = owner.getSnapshot();assert.equal(shown.phase, 'ready');assert.equal(shown.data?.personal.favorite, false);
    await owner.toggle(shown);assert.equal(owner.getSnapshot().data?.personal.favorite, true);
    const command = f.requests.find(request => request.method === 'PUT');assert.equal(command.body.expectedRevision, 4);assert.equal(command.body.favorite, true);
    assert.match(command.body.operationId, /^[a-f0-9-]{36}$/);assert.deepEqual(f.acks,[command.body.operationId]);
    await assert.rejects(owner.toggle(shown), {name: 'AbortError'});assert.equal(f.requests.filter(request => request.method === 'PUT').length, 1);
  } finally {owner.dispose();}
});
test('Favorite retry retains its original operation receipt instead of issuing a replacement toggle', async () => {
  const f = harness(), owner = f.owner();
  try {
    await owner.load();f.failNext();await owner.toggle(owner.getSnapshot());assert.ok(owner.getSnapshot().mutationError);
    await owner.retry(owner.getSnapshot());const commands = f.requests.filter(request => request.method === 'PUT');
    assert.equal(commands.length, 2);assert.deepEqual(commands[0].body, commands[1].body);assert.equal(owner.getSnapshot().data?.personal.favorite, true);
  } finally {owner.dispose();}
});
test('saved Favorite callbacks reject replaced source, run, viewer, collapsed view and retired context', async () => {
  for (const replace of [(f: ReturnType<typeof harness>) => f.replace(), (f: ReturnType<typeof harness>) => f.changeRun(),
    (f: ReturnType<typeof harness>) => f.changeViewer(), (f: ReturnType<typeof harness>) => {f.snapshot.dock.local.expanded = false;}, (f: ReturnType<typeof harness>) => f.retire()]) {
    const f = harness(), owner = f.owner();
    try {await owner.load();const shown = owner.getSnapshot();replace(f);await assert.rejects(owner.toggle(shown), {name: 'AbortError'});assert.equal(f.requests.filter(request => request.method === 'PUT').length, 0);}
    finally {owner.dispose();}
  }
});
test('a late Favorite response after replacement cannot publish into the successor screen', async () => {
  const f = harness(), owner = f.owner();
  try {
    await owner.load();const release = f.hold(), task = owner.toggle(owner.getSnapshot());
    for (let i = 0; i < 20; i++) await Promise.resolve();assert.equal(f.requests.filter(request => request.method === 'PUT').length, 1);
    f.replace();release();await task;assert.equal(owner.current(), false);assert.equal(owner.getSnapshot().data?.personal.favorite, false);assert.deepEqual(f.acks,[]);
  } finally {owner.dispose();}
});


test('Favorite opening reports unexpected rejections while aborts and snapshot-owned errors stay silent', async () => {
  const f = harness(), owner = f.owner(), reported: unknown[] = [];
  try {
    const report = (error: unknown) => reported.push(error);
    reportMusicFavoriteLoadFailure(owner, new DOMException('retired', 'AbortError'), report);
    const owned = {code: 'network', message: 'unavailable', retryable: true};
    reportMusicFavoriteLoadFailure({getSnapshot: () => ({...owner.getSnapshot(), error: owned})}, owned, report);
    assert.deepEqual(reported, []);
    const unexpected = new TypeError('unexpected rejection');
    reportMusicFavoriteLoadFailure(owner, unexpected, report);
    assert.deepEqual(reported, [unexpected]);
    reportMusicFavoriteLoadFailure({getSnapshot: () => ({...owner.getSnapshot(), error: owned})}, unexpected, report);
    assert.deepEqual(reported, [unexpected, unexpected], 'an unrelated snapshot failure cannot hide a new unexpected rejection');
  } finally {owner.dispose();}
});

import test from 'node:test';
import assert from 'node:assert/strict';
import type {LocalSession, PlaybackApi} from '../../../packages/client-core/src/index';
import type {OfflineEntry, OfflineSnapshot} from '../platform/offline-downloads';
import type {LocalMedia, LocalProgress} from '../platform/local-playback';
import {NativeOfflinePlayback} from '../src-next/platform/playback/offline-playback';

function setup() {
  let session: LocalSession | undefined = {accessToken:'throwaway',expiresAt:'2100-01-01T00:00:00Z',sessionFamilyId:'family-1',tokenGeneration:'1',authorizationHorizon:'2100-01-01T00:00:00Z',viewer:{authority:'local',serverId:'server',accountId:'account',profileId:'profile',role:'viewer'}};
  const viewer = session.viewer;
  const entry: OfflineEntry = {id:'copy',itemId:'movie',title:'Movie',subtitle:'',quality:'original',artworkUrl:'',state:'ready',error:'',waiting:'',held:false,bytes:10,bytesDone:10,addedAt:1,onServer:false,changedOnServer:false,
    receipt:{receiptId:'receipt',algorithm:'ed25519',keyId:'key',payload:'fixture',signature:'fixture',revision:1,claims:{kind:'portico.download-receipt',version:'1',receiptId:'receipt',keyId:'key',viewer,itemId:'movie',preparationId:'preparation',quality:'original',qualityLabel:'Original',artifact:{sha256:'a'.repeat(64),bytes:10},issuedAt:'2026-01-01T00:00:00Z',expiresAt:'2100-01-01T00:00:00Z'}}};
  let snapshot: OfflineSnapshot = {supported:true,ready:true,entries:[entry],settings:{wifiOnly:true,deleteWatched:false,autoNext:false,limitBytes:0},freeBytes:100,usedBytes:10,otherBytes:0,pendingProgress:0};
  let reason: ReturnType<import('../platform/offline-downloads').OfflineDownloads['playabilityReason']> = null;
  const media: LocalMedia = {entryId:'copy',itemId:'movie',playId:'play_actual-journal',fileUrl:'file:///private/Downloads/copy/movie.mp4',durationSeconds:600,startSeconds:42};
  let load: () => Promise<LocalMedia> = async () => media;
  const progress: LocalProgress[] = [], network: string[] = [];
  const listeners = new Set<() => void>();
  const owner = new NativeOfflinePlayback(() => session, {getSnapshot:() => snapshot,subscribe:listener => {listeners.add(listener);return () => {listeners.delete(listener);};},playabilityReason:() => reason,localMedia:() => load(),recordProgress:value => progress.push(value)});
  const inner: PlaybackApi = {createPlayback:async id => {network.push('create:'+id);throw new Error('unexpected network start');},progressPlayback:async id => {network.push('progress:'+id);},stopPlayback:async id => {network.push('stop:'+id);}};
  return {owner,entry,media,progress,network,api:owner.wrap(inner),getSession:() => session,setSession:(value:LocalSession|undefined) => {session=value;},setEntries:(entries: readonly OfflineEntry[]) => {snapshot={...snapshot,entries};},setReason:(value:typeof reason) => {reason=value;},setLoad:(value:typeof load) => {load=value;}};
}
const observed = {generation:1,sequence:1,positionSeconds:51,state:'playing' as const};

test('the admitted artifact uses the real local session and journal without a server session', async () => {
  const x = setup(), source = await x.owner.prepare(x.entry, () => true), session = source.session();
  assert.equal(session.id,'local:copy:1'); assert.equal(session.mode,'local');
  assert.equal(session.streamUrl,x.media.fileUrl); assert.equal(session.resumeSeconds,42);
  assert.equal(source.owner.sessionFamilyId,'family-1'); assert.deepEqual(source.artifact,{sha256:'a'.repeat(64),bytes:10});
  await x.api.localPlayback.progressPlayback(session.id,observed); await x.api.localPlayback.stopPlayback(session.id);
  assert.deepEqual(x.network,[]); assert.equal(x.progress.length,1);
  assert.equal(x.progress[0]?.playId,x.media.playId);
  await x.api.progressPlayback('server-session',observed);
  assert.deepEqual(x.network,['progress:server-session']);
});

test('explicit custody retains the actual session generation, sequence and current owner guards', async () => {
  const x = setup(), source = await x.owner.prepare(x.entry, () => true), session = source.session();
  await x.api.localPlayback.progressPlayback('server-session', observed);
  await x.api.localPlayback.stopPlayback('server-session');
  await x.api.localPlayback.progressPlayback(session.id, {...observed, generation: session.generation + 1});
  assert.equal(x.progress.length, 0); assert.deepEqual(x.network, []);
  await x.api.localPlayback.progressPlayback(session.id, observed);
  await x.api.localPlayback.progressPlayback(session.id, {...observed, positionSeconds: 1});
  assert.equal(x.progress.length, 1); assert.equal(x.progress[0]!.positionSeconds, observed.positionSeconds);
  x.setSession({...x.getSession()!, sessionFamilyId: 'replacement'});
  await x.api.localPlayback.progressPlayback(session.id, {...observed, sequence: 2});
  assert.equal(x.progress.length, 1); assert.deepEqual(x.network, []);
});

test('ordinary V1 calls never infer local custody from an admitted session ID', async () => {
  const x = setup(), source = await x.owner.prepare(x.entry, () => true), session = source.session();
  await x.api.progressPlayback(session.id, observed); await x.api.stopPlayback(session.id);
  assert.deepEqual(x.network, [`progress:${session.id}`, `stop:${session.id}`]); assert.equal(x.progress.length, 0);
  await x.api.localPlayback.progressPlayback(session.id, observed);
  assert.equal(x.progress.length, 1); assert.deepEqual(x.network, [`progress:${session.id}`, `stop:${session.id}`]);
});

test('a late file read cannot cross a renewed installation family or a retired caller', async () => {
  for (const change of ['family','caller'] as const) {
    const x = setup(); let resolve!: (value:LocalMedia) => void, current = true;
    x.setLoad(() => new Promise(done => {resolve=done;}));
    const pending = x.owner.prepare(x.entry,() => current);
    if (change === 'family') x.setSession({...x.getSession()!,sessionFamilyId:'family-2'}); else current=false;
    resolve(x.media); await assert.rejects(pending); assert.deepEqual(x.progress,[]);
  }
});

test('a retried local session cannot receive a retired session observation for the same journal play', async () => {
  const x = setup(), source = await x.owner.prepare(x.entry, () => true), original = source.session(), retry = source.session();
  assert.notEqual(original.id, retry.id);
  await x.api.localPlayback.progressPlayback(original.id, {...observed, positionSeconds: 12});
  assert.equal(x.progress.length, 0);
  await x.api.localPlayback.progressPlayback(retry.id, {...observed, generation: retry.generation + 1, positionSeconds: 90});
  assert.equal(x.progress.length, 0);
  await x.api.localPlayback.progressPlayback(retry.id, {...observed, generation: retry.generation, positionSeconds: 51});
  assert.equal(x.progress.length, 1); assert.equal(x.progress[0]!.positionSeconds, 51); assert.equal(x.progress[0]!.playId, x.media.playId);
  await x.api.localPlayback.progressPlayback(retry.id, {...observed, sequence: 1, positionSeconds: 12});
  assert.equal(x.progress.length, 1, 'a duplicate/older observation cannot roll back the same current session');
  await x.api.localPlayback.progressPlayback(retry.id, {...observed, sequence: 2, positionSeconds: 20});
  assert.equal(x.progress.length, 2); assert.equal(x.progress[1]!.positionSeconds, 20, 'a genuinely later seek back remains valid');
  assert.deepEqual(x.network, []);
  x.owner.dispose();
});

test('only the current actual local session projects a complete private-path-free playback fence', async () => {
  const x = setup(), source = await x.owner.prepare(x.entry, () => true), original = source.session();
  const fence = x.owner.fence(original, 3);
  assert.deepEqual(fence, {kind: 'local', playback: {kind: 'local', scope: source.owner, run: 3},
    sessionId: original.id, generation: original.generation, itemId: x.entry.itemId,
    entryId: x.entry.id, playId: x.media.playId, artifact: source.artifact});
  assert.equal(JSON.stringify(fence).includes(x.media.fileUrl), false);
  assert.equal(Object.isFrozen(fence), true); assert.equal(Object.isFrozen(fence!.playback.scope), true);
  assert.equal(x.owner.fence(original, 0), undefined);
  assert.equal(x.owner.fence(original, 4), undefined, 'a following service run cannot relabel the admitted old session');
  assert.equal(x.owner.fence({...original, streamUrl: 'file:///other.mp4'}, 3), undefined);
  assert.equal(x.owner.fence({...original, generation: original.generation + 1}, 3), undefined);
  const retry = source.session();
  assert.equal(x.owner.fence(original, 3), undefined);
  assert.equal(x.owner.fence(retry, 3)?.sessionId, retry.id);
  x.setSession({...x.getSession()!, sessionFamilyId: 'replacement'});
  assert.equal(x.owner.fence(retry, 3), undefined);
  assert.deepEqual(x.network, []);
});

test('native file resolution requires the exact still-admitted local session, not a file-looking source', async () => {
  const x = setup(), source = await x.owner.prepare(x.entry, () => true);
  const original = source.session();
  assert.equal(x.owner.mediaUrl(original), x.media.fileUrl);
  assert.equal(x.owner.mediaUrl({...original, id: 'local:copy:999'}), undefined);
  assert.equal(x.owner.mediaUrl({...original, generation: original.generation + 1}), undefined);
  assert.equal(x.owner.mediaUrl({...original, mode: 'direct'}), undefined);
  assert.equal(x.owner.mediaUrl({...original, streamUrl: 'file:///private/other.mp4'}), undefined);
  const retry = source.session();
  assert.equal(x.owner.mediaUrl(original), undefined);
  assert.equal(x.owner.mediaUrl(retry), x.media.fileUrl);
  x.setEntries([]);
  assert.equal(x.owner.mediaUrl(retry), undefined);
  assert.deepEqual(x.network, []);
});

test('a parked source reopens the actual device entry with a fresh genuine journal play and session', async () => {
  const x = setup(), source = await x.owner.prepare(x.entry, () => true), original = source.session();
  x.setLoad(async () => ({...x.media, playId: 'play_reopened-journal'}));
  const reopened = await x.owner.reopen(source, () => true), session = reopened.session();
  assert.notEqual(session.id, original.id); assert.equal(reopened.media.playId, 'play_reopened-journal');
  assert.equal(x.owner.fence(session, 2)?.playId, 'play_reopened-journal');
  await assert.rejects(x.owner.reopen({...source}, () => true), 'an unowned source object cannot name a bookmark');
  x.setEntries([]); await assert.rejects(x.owner.reopen(reopened, () => true));
  assert.deepEqual(x.network, []);
});

test('same entry IDs cannot authorize replaced artifacts, another viewer or ambiguous rows', async () => {
  for (const change of ['hash','bytes','viewer','duplicate'] as const) {
    const x = setup(), source = await x.owner.prepare(x.entry,() => true), receipt = x.entry.receipt!;
    const claims = {...receipt.claims,...(change==='viewer'?{viewer:{...receipt.claims.viewer,profileId:'other'}}:{}),artifact:{...receipt.claims.artifact,...(change==='hash'?{sha256:'b'.repeat(64)}:change==='bytes'?{bytes:11}:{})}};
    x.setEntries(change==='duplicate'?[x.entry,x.entry]:[{...x.entry,receipt:{...receipt,claims}}]);
    assert.equal(source.current(),false); assert.throws(source.session);
  }
});

test('current receipt expiry or owner loss drops progress before it reaches the offline journal', async () => {
  const x = setup(), source = await x.owner.prepare(x.entry,() => true), session = source.session();
  x.setReason('needs_check_in'); assert.throws(source.session);
  await x.api.localPlayback.progressPlayback(session.id,observed); assert.equal(x.progress.length,0);
  x.setReason(null); x.setSession(undefined);
  await x.api.localPlayback.progressPlayback(session.id,observed); assert.equal(x.progress.length,0); assert.deepEqual(x.network,[]);
});

test('retirement withholds the file immediately but permits only a still-current final journal checkpoint', async () => {
  const x = setup(), source = await x.owner.prepare(x.entry, () => true), session = source.session();
  x.owner.dispose();
  assert.equal(x.owner.mediaUrl(session), undefined); assert.equal(x.owner.fence(session, 1), undefined);
  assert.throws(source.session); await assert.rejects(x.owner.prepare(x.entry, () => true));
  await x.api.localPlayback.progressPlayback(session.id, {...observed, sequence: 1});
  assert.equal(x.progress.length, 0);
  await x.api.localPlayback.progressPlayback(session.id, {...observed, sequence: 2, state: 'paused'});
  assert.equal(x.progress.length, 1); assert.equal(x.progress[0]!.state, 'paused');
  x.setSession({...x.getSession()!, sessionFamilyId: 'replacement'});
  await x.api.localPlayback.progressPlayback(session.id, {...observed, sequence: 3, state: 'ended'});
  assert.equal(x.progress.length, 1); assert.deepEqual(x.network, []);
});

test('same-artifact receipt renewal stays usable; disposal and nonlocal addresses cannot admit playback', async () => {
  const x = setup(), source = await x.owner.prepare(x.entry,() => true);
  x.setEntries([{...x.entry,receipt:{...x.entry.receipt!,revision:2,payload:'renewed',signature:'renewed'}}]);
  assert.equal(source.current(),true); assert.equal(source.session().mode,'local');
  x.owner.dispose(); assert.equal(source.current(),false); assert.throws(source.session);
  for (const fileUrl of ['https://example.invalid/movie','file://other-host/movie','file:///movie?token=not-local']) {
    const y = setup(); y.setLoad(async () => ({...y.media,fileUrl}));
    await assert.rejects(y.owner.prepare(y.entry,() => true));
  }
});

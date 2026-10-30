import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ClientCommandJournal, JournalConflict, laneStorageKey, type AtomicCommandStore, type IntentDraft, type LaneScope, type LaneState, type PreparedCommand } from '../../src/playback/command-journal.ts';
import type { Capabilities, DesiredState } from '../../src/playback/protocol.ts';

/** Independent transaction model: publication occurs only after work+durability succeeds. */
class AtomicMemoryStore implements AtomicCommandStore {
  lanes = new Map<string, LaneState>();
  commands = new Map<string, PreparedCommand[]>();
  failCommit = false;
  async transaction<T>(key: string, work: Parameters<AtomicCommandStore['transaction']>[1]): Promise<T> {
    const lane = structuredClone(this.lanes.get(key) ?? null);
    const commands = structuredClone(this.commands.get(key) ?? []);
    let nextLane = lane;
    const result = work({ lane, findMutation: id => commands.find(c => c.localMutationId === id) ?? null,
      findRequest: id => commands.find(c => c.requestId === id) ?? null,
      putLane: value => { nextLane = structuredClone(value); },
      addCommand: value => { commands.push(structuredClone(value)); },
    });
    if (this.failCommit) throw new Error('durability failure');
    if (nextLane) this.lanes.set(key, nextLane);
    this.commands.set(key, commands);
    return structuredClone(result) as T;
  }
}
const scope: LaneScope = { serverOrigin:'http://localhost:19507',accountId:'account',profileId:'profile',
  controllerId:'phone',controllerEpoch:'epoch',commandLaneId:'movie' };
const context = { itemId:'item',editionId:null,partId:null,mappingId:null };
const desired: DesiredState = { context,source:{mode:'automatic',sourceId:null,sourceVersionId:null},
  audio:{mode:'automatic',trackId:null},subtitles:{track:{mode:'off',trackId:null},renderMode:'automatic'},
  quality:{mode:'original',maxBitrateBps:null,maxWidth:null,maxHeight:null,allowLossyConversion:false,allowHDRToSDR:false},
  state:'playing',rate:{numerator:'1',denominator:'1'},seek:{seekCommandId:'seek180',target:{context,positionUs:'180000000'}},offerSelection:null };
const capabilities: Capabilities = { profileVersion:'native',engine:'AVPlayer',engineVersion:'1',os:'ios',osVersion:'26',
  outputRouteId:'speaker',outputRouteRevision:'1',transports:[],candidatePreparation:'serial_attach',backgroundControl:false,evidence:'declared' };
const expected = { playbackRevision:'1',ownershipRevision:'1',presentationGeneration:null,presentationRevision:null };
const create: IntentDraft = {operation:'create',playbackId:null,body:{protocolVersion:'2.0',desired,capabilities,replaces:null}};
const pause: IntentDraft = {operation:'intent',playbackId:'occurrence',body:{protocolVersion:'2.0',expected,desired:{...desired,state:'paused'},capabilities}};
const stop: IntentDraft = {operation:'stop',playbackId:'occurrence',body:{protocolVersion:'2.0',expected}};
const conflict = (code: JournalConflict['code']) => (e: unknown) => e instanceof JournalConflict && e.code === code;
async function setup() { const store = new AtomicMemoryStore(); const journal = new ClientCommandJournal(store); await journal.initialize(scope); return {store,journal}; }

test('durability failure publishes neither sequence nor desired state', async () => {
  const {store,journal}=await setup(); store.failCommit=true;
  await assert.rejects(journal.prepare(scope,'0','m1','r1',create),/durability failure/);
  store.failCommit=false; assert.equal((await journal.read(scope))?.highestAllocated,'0');
  assert.equal((await journal.prepare(scope,'0','m1','r1',create)).sequence,'1');
});
test('atomic Pause wins over a stale complete JS playing snapshot and preserves seek',async()=>{
  const {journal}=await setup(); await journal.prepare(scope,'0','m1','r1',create);
  await journal.prepare(scope,'1','osPause','r2',pause);
  await assert.rejects(journal.prepare(scope,'1','jsOld','r3',{...pause,body:{...pause.body,desired}}),conflict('local_revision'));
  const lane=await journal.read(scope); assert.equal(lane?.desired?.state,'paused');
  assert.deepEqual(lane?.desired?.seek,desired.seek); assert.equal(lane?.highestAllocated,'2');
});
test('concurrent mutations at the same revision allocate exactly one command',async()=>{
  const {journal}=await setup(); await journal.prepare(scope,'0','m1','r1',create);
  const outcomes=await Promise.allSettled([journal.prepare(scope,'1','m2','r2',pause),journal.prepare(scope,'1','m3','r3',pause)]);
  assert.equal(outcomes.filter(x=>x.status==='fulfilled').length,1);
  assert.equal((await journal.read(scope))?.highestAllocated,'2');
});
test('exact replay returns original command without restoring old desired state',async()=>{
  const {journal}=await setup(); const original=await journal.prepare(scope,'0','m1','r1',create);
  await journal.prepare(scope,'1','m2','r2',pause);
  assert.deepEqual(await journal.prepare(scope,'0','m1','r1',create),original);
  assert.equal((await journal.read(scope))?.desired?.state,'paused');
  await assert.rejects(journal.prepare(scope,'2','m1','r1',pause),conflict('mutation_identity'));
  await assert.rejects(journal.prepare(scope,'2','m3','r2',pause),conflict('request_identity'));
});
test('Stop persists terminal fence without any bookmark or transport dependency',async()=>{
  const {journal}=await setup(); await journal.prepare(scope,'0','m1','r1',create);
  const command=await journal.prepare(scope,'1','close','r2',stop);
  const envelope=JSON.parse(command.envelopeCanonical); assert.equal(envelope.targetId,'occurrence');
  assert.equal(envelope.operation,'stop'); assert.equal((await journal.read(scope))?.terminal,true);
  await assert.rejects(journal.prepare(scope,'2','late','r3',pause),conflict('terminal'));
});
test('independent lanes have independent counters and cannot reset through initialize',async()=>{
  const {journal}=await setup(); const other={...scope,commandLaneId:'music'};
  await journal.initialize(other); await journal.prepare(scope,'0','m1','r1',create);
  await journal.prepare(scope,'1','m2','r2',pause);
  assert.equal((await journal.prepare(other,'0','m1','r1',create)).sequence,'1');
  assert.equal((await journal.initialize(scope)).highestAllocated,'2');
});
test('retired lane rejects new work; reads cannot mutate persisted desired state',async()=>{
  const {journal}=await setup(); await journal.prepare(scope,'0','m1','r1',create);
  const read=await journal.read(scope); (read!.desired as {state:string}).state='paused';
  assert.equal((await journal.read(scope))?.desired?.state,'playing');
  await journal.retire(scope,'1'); await assert.rejects(journal.prepare(scope,'2','m2','r2',pause),conflict('retired'));
});

test('counter exhaustion cannot wrap or partially publish desired state',async()=>{
  const {store,journal}=await setup();
  const key=laneStorageKey(scope), old=store.lanes.get(key)!;
  store.lanes.set(key,{...old,highestAllocated:'9223372036854775807'});
  await assert.rejects(journal.prepare(scope,'0','m1','r1',create),/counter exhaustion/);
  assert.equal((await journal.read(scope))?.revision,'0');
  assert.equal((await journal.read(scope))?.desired,null);
});

test('rejected Stop can reconcile with a fresh ordered Stop while late Play stays fenced',async()=>{
  const {journal}=await setup(); await journal.prepare(scope,'0','m1','r1',create);
  const first=await journal.prepare(scope,'1','close1','r2',stop);
  // Transport reports revision_conflict; immutable original replay must remain original.
  assert.deepEqual(await journal.prepare(scope,'1','close1','r2',stop),first);
  const reconciled:IntentDraft={...stop,body:{...stop.body,expected:{...expected,playbackRevision:'4'}}};
  const retry=await journal.prepare(scope,'2','close2','r3',reconciled);
  assert.equal(retry.sequence,'3');
  assert.equal(JSON.parse(retry.envelopeCanonical).body.expected.playbackRevision,'4');
  assert.equal((await journal.read(scope))?.terminal,true);
  await assert.rejects(journal.prepare(scope,'3','late','r4',pause),conflict('terminal'));
});

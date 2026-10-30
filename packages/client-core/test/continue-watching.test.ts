import test from 'node:test';
import assert from 'node:assert/strict';
import {personalRevision, setContinueDismissed} from '../src/continue-watching.ts';

type Call = {path: string; method?: string; body?: any};
function fakeApi(answers: Array<(call: Call) => unknown>) {
  const calls: Call[] = [];
  return {
    calls,
    async request<T>(path: string, method?: string, body?: unknown): Promise<T> {
      const call = {path, method, body};
      calls.push(call);
      const next = answers.shift();
      if (!next) throw new Error('unexpected request ' + path);
      return next(call) as T;
    },
  };
}
const conflict = () => { throw Object.assign(new Error('conflict'), {code: 'personal_state_conflict', status: 409}); };
let n = 0;
const operationId = () => 'op-' + ++n;
const receipt = (call: Call, revision: number) => ({operationId: call.body.operationId, serverId:'server',libraryId:'library',itemId:decodeURIComponent(call.path.split('/')[3]),viewerFence:'fence',personal:{revision}});

test('dismisses with the revision the caller holds, as the server’s continueDismissed field', async () => {
  const api = fakeApi([call => receipt(call,8)]);
  const out = await setContinueDismissed(api, {itemId: 'item 1', dismissed: true, revision: 7, operationId});
  assert.deepEqual(out, {revision: 8});
  assert.equal(api.calls.length, 1);
  assert.equal(api.calls[0].path, '/v1/items/item%201/personal-state');
  assert.equal(api.calls[0].method, 'PUT');
  assert.deepEqual(Object.keys(api.calls[0].body).sort(), ['continueDismissed', 'expectedRevision', 'operationId']);
  assert.equal(api.calls[0].body.continueDismissed, true);
  assert.equal(api.calls[0].body.expectedRevision, 7);
  assert.equal('watched' in api.calls[0].body, false, 'never Mark Unwatched: the resume point stays');
});

test('reads the revision from the title’s detail when the caller has none', async () => {
  const api = fakeApi([() => ({personal: {revision: 3}}), call => receipt(call,4)]);
  const out = await setContinueDismissed(api, {itemId: 'a', dismissed: false, operationId});
  assert.equal(api.calls[0].path, '/v1/items/a/detail');
  assert.equal(api.calls[1].body.expectedRevision, 3);
  assert.equal(api.calls[1].body.continueDismissed, false);
  assert.deepEqual(out, {revision: 4});
});

test('a covered conflict preserves the authored revision and never sends a replacement operation', async () => {
  const api = fakeApi([conflict, () => ({personal: {revision: 12}}), call => receipt(call,13)]);
  const before=n;
  await assert.rejects(setContinueDismissed(api, {itemId: 'a', dismissed: true, revision: 10, operationId}), {code:'personal_state_conflict'});
  assert.equal(api.calls.length,1);assert.equal(n,before+1);
  assert.equal(api.calls[0].body.expectedRevision,10);
});

test('a conflict or any other failure is the caller’s', async () => {
  await assert.rejects(setContinueDismissed(fakeApi([conflict, () => ({personal: {revision: 1}}), conflict]), {itemId: 'a', dismissed: true, revision: 0, operationId}), {code: 'personal_state_conflict'});
  const refused = () => { throw Object.assign(new Error('nope'), {code: 'forbidden'}); };
  const api = fakeApi([refused]);
  await assert.rejects(setContinueDismissed(api, {itemId: 'a', dismissed: true, revision: 0, operationId}), {code: 'forbidden'});
  assert.equal(api.calls.length, 1);
});

test('operation IDs outside the server’s alphabet are refused before sending', async () => {
  const api = fakeApi([]);
  await assert.rejects(setContinueDismissed(api, {itemId: 'a', dismissed: true, revision: 0, operationId: () => 'has space'}));
  assert.equal(api.calls.length, 0);
});

test('personalRevision reads only a whole, non-negative revision', () => {
  assert.equal(personalRevision({personal: {revision: 5}}), 5);
  assert.equal(personalRevision({personal: {revision: -1}}), undefined);
  assert.equal(personalRevision({personal: {revision: 1.5}}), undefined);
  assert.equal(personalRevision(null), undefined);
});


test('acknowledgement follows only an exact validated personal receipt', async () => {
  for(const change of ['none','operation','item','revision','missing']) {
    const acks:string[]=[];
    const api={...fakeApi([call=>{const raw=receipt(call,2);if(change==='operation')raw.operationId='other';if(change==='item')raw.itemId='other';if(change==='revision')raw.personal.revision=-1;return change==='missing'?{}:raw;}]),acknowledgeUserMutation:async(key:string)=>{acks.push(key);}};
    const pending=setContinueDismissed(api,{itemId:'a',dismissed:true,revision:1,operationId});
    if(change==='none'){assert.deepEqual(await pending,{revision:2});assert.deepEqual(acks,[api.calls[0].body.operationId]);}
    else {await assert.rejects(pending,{code:'invalid_detail'});assert.deepEqual(acks,[]);}
  }
});


const compact = () => ({key:'p251_1793200000000_0123456789abcdef0123456789abcdef',operation:'put_items_id_personal_state',status:'applied',targetId:'a',revision:2,committedMs:1793200000000,expiresMs:1795792000000});
test('compact dismissal recovery consumes current detail even when its value changed after commit', async () => {
  const acks:string[]=[];
  const api={...fakeApi([()=>({mutationReceipt:compact()}),()=>({scope:{serverId:'server',libraryId:'library',itemId:'a',viewerFence:'fence'},item:{id:'a',libraryId:'library'},personal:{revision:5,continueDismissed:false}})]),acknowledgeUserMutation:async(key:string)=>{acks.push(key);}};
  assert.deepEqual(await setContinueDismissed(api,{itemId:'a',dismissed:true,revision:1,operationId}),{revision:5});
  assert.deepEqual(acks,[api.calls[0].body.operationId]);assert.equal(api.calls.length,2);assert.equal(api.calls[1].method,'GET');
});
test('compact dismissal recovery never acknowledges a wrong target or unreadable current detail', async () => {
  for(const wrongTarget of [false,true]) {
    const acks:string[]=[];const marker=compact();if(wrongTarget)marker.targetId='other';
    const api={...fakeApi([()=>({mutationReceipt:marker}),()=>({personal:{revision:5}})]),acknowledgeUserMutation:async(key:string)=>{acks.push(key);}};
    await assert.rejects(setContinueDismissed(api,{itemId:'a',dismissed:true,revision:1,operationId}));assert.deepEqual(acks,[]);
  }
});

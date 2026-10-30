import test from 'node:test';
import assert from 'node:assert/strict';
import {screenLifetime, type ScreenContext} from '../src/screens/detail/context.ts';
import {createDetailBindings} from '../src/screens/detail/bindings.ts';
import {createShowBindings} from '../src/screens/detail/show-bindings.ts';

// The route owns the binding and disposes it; the screen below it cleans up
// afterwards. That cleanup must not reach a retired service.
const context = {
  api: {
    request: async () => { throw new Error('no read expected'); },
    requestRaw: async () => ({status: 200, headers: {}, body: {}}),
  },
  scope: {serverId: 'server', viewerId: JSON.stringify(['local', 'account', 'profile'])},
  isCurrent: () => true,
  preferences: {getSnapshot: () => ({}), subscribe: () => () => {}},
  effects: {openEntry: () => {}, openSettings: () => {}, play: async () => {}, reportError: () => {}},
} as unknown as ScreenContext;

test('a title binding tolerates the screen cleanup that follows its dispose', () => {
  const detail = createDetailBindings(context);
  detail.dispose();
  assert.doesNotThrow(() => detail.cancel());
  assert.doesNotThrow(() => detail.cancel());
  // Binding idempotence does not weaken the service's own guard.
  assert.throws(() => detail.service.cancel(), /disposed/);
});

test('a live title binding still cancels its read', () => {
  const detail = createDetailBindings(context);
  assert.doesNotThrow(() => detail.cancel());
  assert.equal(detail.service.getSnapshot().phase, 'idle');
  detail.dispose();
});

test('a show binding tolerates the screen cleanup that follows its dispose', () => {
  const show = createShowBindings(context);
  show.dispose();
  assert.doesNotThrow(() => show.cancel());
  assert.throws(() => show.service.cancel(), /disposed/);
});


test('screen lifetime forwards optional acknowledgement under its captured authority', async () => {
  let current=true;const acks:string[]=[];
  const source={...context,isCurrent:()=>current,api:{...context.api,acknowledgeUserMutation:async(id:string)=>{acks.push(id);}}} as ScreenContext;
  const life=screenLifetime(source);
  await life.api.acknowledgeUserMutation?.('original');assert.deepEqual(acks,['original']);
  current=false;await assert.rejects(life.api.acknowledgeUserMutation!('retired'),{name:'AbortError'});assert.deepEqual(acks,['original']);life.dispose();
  const optional=screenLifetime(context);await optional.api.acknowledgeUserMutation?.('legacy');optional.dispose();
});

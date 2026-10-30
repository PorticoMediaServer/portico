import assert from 'node:assert/strict';
import test from 'node:test';
import {AccountSecurityJournal} from '../src/platform/account-security-journal.ts';
import {clone, hostedFixture, iso} from './support/account-security-journal-fixture.ts';

// ACC-LEAF-26: predecessor, immutable-original and CAS boundaries, authored UNRUN.
// Each held vector pauses at the real seam it targets (before the updater, before a
// read, or after the updater but before the final commit) and mutates a caller-owned
// clone; records the class returns are frozen.

function harness(initial?: unknown) {
  let value: unknown = initial === undefined ? undefined : clone(initial), writes = 0;
  let held: 'update' | 'read' | 'commit' | undefined, release: (() => void) | undefined;
  let entered: (() => void) | undefined;
  const gate = async (at: typeof held) => {
    if (held !== at) return;
    await new Promise<void>(resolve => {release = resolve;entered?.();});
  };
  const store = {
    read: async () => {await gate('read');return value;},
    change: async (update: (current: unknown) => unknown, beforeCommit?: () => void) => {
      await gate('update');
      const next = update(value);
      await gate('commit');
      beforeCommit?.();value = next;writes++;
    },
    assertAvailable: () => {},
  };
  return {
    journal: new AccountSecurityJournal(store),
    get value() {return value;},get writes() {return writes;},
    hold: (at: 'update' | 'read' | 'commit') => {held=at;return new Promise<void>(resolve=>{entered=resolve;});},
    release: () => {held=undefined;release?.();},
  };
}

test('retireAccount removes only the identical original and never successor B', async () => {
  const f = hostedFixture();
  const h = harness(f.enriched);
  await h.journal.retireAccount(f.original);
  assert.equal(h.value, undefined);
  const b = harness({original: f.successor});
  await b.journal.retireAccount(f.original);
  assert.deepEqual(b.value, {original: f.successor});
});

test('a late clear or replace of A preserves resident successor B', async () => {
  const f = hostedFixture();
  const h = harness({original: f.successor});
  await h.journal.clear(clone(f.pending));
  assert.deepEqual(h.value, {original: f.successor});
  await assert.rejects(h.journal.replace(clone(f.pending), clone(f.enriched), () => {}));
  assert.deepEqual(h.value, {original: f.successor});
});

test('same-original enrichment is accepted and returns a frozen record', async () => {
  const f = hostedFixture();
  const h = harness(f.pending);
  const out = await h.journal.replace(clone(f.pending), clone(f.enriched), () => {});
  assert.equal(Object.isFrozen(out), true);
  assert.deepEqual(h.value, f.enriched);
  await h.journal.clear(clone(f.pending));
  assert.deepEqual(h.value, f.enriched, 'an old complete predecessor cannot clear an enriched receipt');
});

test('same-opening purpose, target, provider, operation or family mutation is refused', async () => {
  const f = hostedFixture();
  const email = clone(f.pending);
  email.original.intent = {purpose: 'email_change', target: 'old@example.test', operationId: 'operation_A_0000000000'};
  const purpose = clone(email);
  purpose.original.intent = {purpose: 'mfa_disable', target: '', operationId: 'operation_A_0000000000'};
  const target = clone(email);
  target.original.intent = {...target.original.intent, target: 'new@example.test'};
  const operation = clone(f.pending);
  operation.original.intent.operationId='operation_other_0000000000';

  const second = {id: 'identity_second', provider: 'apple' as const, revision: '2'};
  const providerBase = clone(f.pending);
  providerBase.original.state.providers = [...providerBase.original.state.providers, second];
  const provider = clone(providerBase);
  provider.original.provider = second;

  // The original predecessor stays untouched; only the successor moves family,
  // through session, state and both coordinator families together.
  const family = clone(f.pending);
  family.original.session.familyId = 'family_other';
  family.original.state.familyId = 'family_other';
  const coordinator = JSON.parse(family.original.coordinator);
  coordinator.active.session.familyId = 'family_other';
  coordinator.active.authority.familyId = 'family_other';
  family.original.coordinator = JSON.stringify(coordinator);

  for (const [previous, next] of [[email, purpose], [email, target], [providerBase, provider], [f.pending, operation], [f.pending, family]] as const) {
    const h = harness(previous);
    await assert.rejects(h.journal.replace(clone(previous), clone(next), () => {}), JSON.stringify(next.original.intent));
    assert.deepEqual(h.value, previous);
  }
});

// The complete comparison operand changes from A to B. Changing just one ID
// would not make the old buggy implementation match B and would hide the bug.
for (const getters of [false, true]) for (const operation of ['clear', 'retireAccount', 'assertOriginal', 'replace'] as const) {
  test(`${operation} captures its complete ${getters ? 'getter-backed' : 'mutable'} operand before storage waits`, async () => {
    const f=hostedFixture(), h=harness({original:f.successor});
    let backing=clone(f.original), reads=0;
    const supplied=clone(f.pending);
    if(getters) {
      const keys=Object.keys(backing) as (keyof typeof backing)[];
      for(const key of keys) Object.defineProperty(supplied.original,key,{enumerable:true,get:()=>{reads++;return backing[key];}});
    }
    const entered=h.hold(operation==='assertOriginal'?'read':'update');
    const running=operation==='clear'?h.journal.clear(supplied):operation==='retireAccount'?h.journal.retireAccount(supplied.original):operation==='assertOriginal'?h.journal.assertOriginal(supplied):h.journal.replace(supplied,clone(f.enriched),()=>{});
    // Attach the rejection observer before releasing the held source.
    const result=running.then(()=>({ok:true as const}),error=>({ok:false as const,error}));
    await entered;
    const copiedReads=reads;
    if(getters)backing=clone(f.successor);else Object.assign(supplied.original,clone(f.successor));
    h.release();
    const finished=await result;
    assert.equal(finished.ok,operation==='clear'||operation==='retireAccount');
    assert.deepEqual(h.value,{original:f.successor});
    if(getters){assert.ok(copiedReads>0);assert.equal(reads,copiedReads,'the caller getters are never reread after the wait');}
  });
}

for(const operation of ['create','replace'] as const) {
  test(`${operation} repeats the mounted lifetime at its final commit`,async()=>{
    const f=hostedFixture(), h=harness(operation==='replace'?f.pending:undefined);
    let live=true;
    const entered=h.hold('commit'), assertCurrent=()=>{if(!live)throw new Error('The original continuation lifetime ended.');};
    const running=operation==='create'?h.journal.create(clone(f.enriched),assertCurrent):h.journal.replace(clone(f.pending),clone(f.enriched),assertCurrent);
    const refused=assert.rejects(running,/lifetime ended/);
    await entered;live=false;h.release();await refused;
    assert.equal(h.writes,0);
    assert.deepEqual(h.value,operation==='replace'?f.pending:undefined);
  });
  test(`${operation} repeats its finite deadline at the final commit`,async()=>{
    const f=hostedFixture(), h=harness(operation==='replace'?f.pending:undefined);
    const entered=h.hold('commit');
    const running=operation==='create'?h.journal.create(clone(f.enriched),()=>{}):h.journal.replace(clone(f.pending),clone(f.enriched),()=>{});
    const refused=assert.rejects(running);
    await entered;
    const now=Date.now;
    try {
      Date.now=()=>Date.parse(f.original.expiresAt)+1;
      h.release();await refused;
    } finally {Date.now=now;}
    assert.equal(h.writes,0);
    assert.deepEqual(h.value,operation==='replace'?f.pending:undefined);
  });
}

test('expired A may be cleaned up exactly while an ordinary read refuses it', async () => {
  const f = hostedFixture();
  const expired = clone(f.pending);
  expired.original.createdAt = iso(-300000);
  expired.original.expiresAt = iso(-1000);
  const h = harness(expired);
  await assert.rejects(h.journal.read());
  await h.journal.clear(clone(expired));
  assert.equal(h.value, undefined);
});

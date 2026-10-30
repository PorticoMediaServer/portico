import test from 'node:test';
import assert from 'node:assert/strict';
import {createScreenPlayEffect, createScreenSeasonPlayEffect} from '../src/screens/browse/playback-effect.ts';

function deferred() {let resolve!: () => void;const promise = new Promise<void>(done => {resolve = done;});return {promise, resolve};}
const target = {libraryId: 'library', itemId: 'item', startSeconds: 12};

test('the screen effect forwards its actual target, original signal and captured currentness', async () => {
  let calls = 0;
  const signal = new AbortController().signal;
  const runtime = {async playFromScreen(actual: typeof target, lifetime: {signal: AbortSignal; isCurrent: () => boolean}) {
    calls++;assert.equal(actual, target);assert.equal(lifetime.signal, signal);assert.equal(lifetime.isCurrent(), true);
  }};
  const effect = createScreenPlayEffect({isCurrent: () => true, runtime: () => runtime});
  await effect(target, signal);assert.equal(calls, 1);
  await assert.rejects(effect(target, signal, () => false), {name: 'AbortError'});assert.equal(calls, 1);
});

test('caller, authority, original abort and runtime replacement retire a held screen start', async () => {
  for (const change of ['caller', 'authority', 'abort', 'runtime']) {
    const held = deferred(), abort = new AbortController();let caller = true, authority = true, guard: (() => boolean) | undefined, replacementCalls = 0;
    const first = {async playFromScreen(_target: typeof target, lifetime: {signal: AbortSignal; isCurrent: () => boolean}) {
      assert.equal(lifetime.signal, abort.signal);guard = lifetime.isCurrent;await held.promise;
    }};
    const replacement = {async playFromScreen() {replacementCalls++;}};
    let runtime: typeof first | typeof replacement = first;
    const effect = createScreenPlayEffect({isCurrent: () => authority, runtime: () => runtime}), pending = effect(target, abort.signal, () => caller);
    assert.equal(guard?.(), true);
    if (change === 'caller') caller = false;
    else if (change === 'authority') authority = false;
    else if (change === 'abort') abort.abort();
    else runtime = replacement;
    assert.equal(guard?.(), false);held.resolve();await assert.rejects(pending, {name: 'AbortError'});assert.equal(replacementCalls, 0);
  }
});

test('an unavailable runtime refuses before starting and callback receivers remain captured', async () => {
  let calls = 0;
  const input = {active: true, isCurrent() {return this.active;}, runtime() {return {playFromScreen: async () => {calls++;}};}};
  const runtime = input.runtime();input.runtime = () => runtime;
  const effect = createScreenPlayEffect(input);
  await effect(target, new AbortController().signal);assert.equal(calls, 1);
  input.active = false;await assert.rejects(effect(target, new AbortController().signal), {name: 'AbortError'});assert.equal(calls, 1);
  // P2-74 E-web-02: no player yet is a visible refusal, not a silent cancel.
  await assert.rejects(createScreenPlayEffect({isCurrent: () => true, runtime: () => undefined})(target, new AbortController().signal), {code: 'operation_unavailable'});
});


test('season effect sends only the exact complete-season selector with original lifetime',async()=>{
 const signal=new AbortController().signal;let calls=0;
 const runtime={async playFromQueue(source: unknown,lifetime:{signal:AbortSignal;isCurrent:()=>boolean}){calls++;assert.deepEqual(source,{container:{kind:'season',id:'selected-season'}});assert.equal(lifetime.signal,signal);assert.equal(lifetime.isCurrent(),true);}};
 const effect=createScreenSeasonPlayEffect({isCurrent:()=>true,runtime:()=>runtime});
 await effect('selected-season',signal,()=>true);assert.equal(calls,1);
 await assert.rejects(effect('selected-season',signal,()=>false),{name:'AbortError'});assert.equal(calls,1);
});

test('season effect refuses retired caller, authority, abort and replaced runtime without adopting replacement',async()=>{
 for(const change of ['caller','authority','abort','runtime']){
  const held=deferred(),abort=new AbortController();let caller=true,authority=true,guard:(()=>boolean)|undefined,replacementCalls=0;
  const first={async playFromQueue(source:unknown,lifetime:{signal:AbortSignal;isCurrent:()=>boolean}){assert.deepEqual(source,{container:{kind:'season',id:'season'}});assert.equal(lifetime.signal,abort.signal);guard=lifetime.isCurrent;await held.promise;}};
  const replacement={async playFromQueue(){replacementCalls++;}};let runtime:typeof first|typeof replacement=first;
  const pending=createScreenSeasonPlayEffect({isCurrent:()=>authority,runtime:()=>runtime})('season',abort.signal,()=>caller);
  assert.equal(guard?.(),true);
  if(change==='caller')caller=false;else if(change==='authority')authority=false;else if(change==='abort')abort.abort();else runtime=replacement;
  assert.equal(guard?.(),false);held.resolve();await assert.rejects(pending,{name:'AbortError'});assert.equal(replacementCalls,0);
 }
 await assert.rejects(createScreenSeasonPlayEffect({isCurrent:()=>true,runtime:()=>undefined})('season',new AbortController().signal,()=>true),{code:'operation_unavailable'});
});

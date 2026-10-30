import {test} from 'node:test';
import assert from 'node:assert/strict';
import {createDesktopEqualizerPort} from '../src/equalizer-port.ts';
import type {EqualizerSnapshot} from '../src/equalizer.ts';
const initial:EqualizerSnapshot={revision:41,settings:{enabled:true,preampDb:-3,bandsDb:[0,0,0,0,0,0,0,0,0,0]},applied:false};
const tick=()=>new Promise<void>(resolve=>setImmediate(resolve));

test('native EQ UI waits for actual CAS acknowledgement and Reset keeps the actual switch',async()=>{
  let actual=initial,acknowledge!:(value:EqualizerSnapshot)=>void;
  const revisions:number[]=[];
  const port=createDesktopEqualizerPort(()=>assert.fail('unexpected read failure'),{
    async read(){return actual},
    set(expected,settings){revisions.push(expected);return new Promise(resolve=>{acknowledge=value=>{actual=value;resolve(value);};});},
    async resetToFlat(expected){revisions.push(expected);return actual={...actual,revision:73,settings:{enabled:actual.settings.enabled,preampDb:0,bandsDb:[0,0,0,0,0,0,0,0,0,0]}}},
  });
  const stop=port.start();await tick();
  const next={...initial.settings,preampDb:-6};const pending=port.change(next);await tick();
  assert.equal(port.getSnapshot()?.preampDb,-3);
  acknowledge({...initial,revision:72,settings:next});await pending;
  assert.equal(port.getSnapshot()?.preampDb,-6);
  await port.reset();assert.equal(port.getSnapshot()?.enabled,true);assert.equal(port.getSnapshot()?.preampDb,0);
  assert.deepEqual(revisions,[41,72]);stop();
});

test('native EQ conflict reloads committed state and never overwrites it with a guessed revision',async()=>{
  let actual=initial,writes=0;
  const port=createDesktopEqualizerPort(()=>assert.fail('unexpected read failure'),{
    async read(){return actual},
    async set(){++writes;actual={...initial,revision:55,settings:{...initial.settings,preampDb:2}};throw new Error('preferences_conflict')},
    async resetToFlat(){throw new Error('unused')},
  });
  const stop=port.start();await tick();
  await assert.rejects(port.change({...initial.settings,preampDb:-9}),/preferences_conflict/);
  assert.equal(writes,1);assert.equal(port.getSnapshot()?.preampDb,2);stop();
});

test('a late mount read cannot replace a newer committed EQ acknowledgement',async()=>{
  let reads=0,complete!:(value:EqualizerSnapshot)=>void;
  const port=createDesktopEqualizerPort(()=>assert.fail('unexpected read failure'),{
    read(){return ++reads===1?new Promise(resolve=>{complete=resolve}):Promise.resolve(initial)},
    async set(_,settings){return {...initial,revision:42,settings}},async resetToFlat(){return initial},
  });
  let publications=0;const off=port.subscribe(()=>{++publications}),stop=port.start();
  await port.change({...initial.settings,preampDb:1});complete(initial);await tick();
  assert.equal(port.getSnapshot()?.preampDb,1);assert.equal(publications,1);stop();off();
});

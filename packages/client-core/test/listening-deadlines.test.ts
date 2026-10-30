import test from 'node:test';
import assert from 'node:assert/strict';
import {ListeningDeadlineClock} from '../src/playback/native-listening.ts';
test('sleep and passout are independent absolute deadlines, not rerender durations',()=>{
 let wall=1000,mono=0;const fired:string[]=[];const clock=new ListeningDeadlineClock(k=>fired.push(k),()=>wall,()=>mono);
 clock.set({sleepAtMs:2000,passoutAtMs:3000});wall=1500;mono=500;clock.set(clock.getSnapshot());
 wall=1200;mono=1000;clock.check();assert.deepEqual(fired,['sleepAtMs']);assert.equal(clock.getSnapshot().passoutAtMs,3000);
 mono=2000;clock.check();clock.check();assert.deepEqual(fired,['sleepAtMs','passoutAtMs']);clock.clear();
});
test('foreground catches suspended/forward wall clock; off and clear remove pending timers',()=>{
 let wall=1000,mono=0;const fired:string[]=[];const clock=new ListeningDeadlineClock(k=>fired.push(k),()=>wall,()=>mono);
 clock.set({sleepAtMs:2000,passoutAtMs:3000});clock.set({sleepAtMs:null,passoutAtMs:3000});wall=4000;clock.check();assert.deepEqual(fired,['passoutAtMs']);
 clock.set({sleepAtMs:5000,passoutAtMs:null});clock.clear();wall=6000;mono=10000;clock.check();assert.equal(fired.length,1);
 assert.throws(()=>clock.set({sleepAtMs:NaN,passoutAtMs:null}));assert.throws(()=>clock.set({sleepAtMs:0,passoutAtMs:null}));
});
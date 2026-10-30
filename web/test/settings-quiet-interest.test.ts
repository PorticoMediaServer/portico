import test from 'node:test';
import assert from 'node:assert/strict';
import {FreshnessHub,dependsOn} from '@core/freshness-hub.ts';
import {DraftForm} from '@core/server-admin/draft-form.ts';
import {mountQuietInput,quietInterestOutcome,createSettingsOpen} from '../src/screens/settings/quiet-refresh.ts';
import {SettingsSectionReads} from '../src/screens/settings/section-loading.ts';
import {quietClock,deferred,tick} from './settings-quiet-clock.ts';
const deps=[dependsOn.settings('server')];

test('mounted partial inputs keep separate retry floors and preserve section acknowledgement',async()=>{
 const c=quietClock(),hub=new FreshnessHub(c.env),error=Object.assign(new Error('busy'),{retryAfterSeconds:7200});
 const section=new SettingsSectionReads(()=>true,()=>{});await section.load('server/general',[{id:'a',load:async()=>{}}]);section.update({saveState:'saved'});const before=section.snapshot('server/general');
 let a=0,b=0;const first=mountQuietInput(hub,'settings/general',deps,{id:'a',refresh:async()=>{a++;return {kind:'adopted'};}});
 const second=mountQuietInput(hub,'settings/general',deps,{id:'b',refresh:async()=>{b++;return {kind:'unreadable',error};}});
 hub.note(deps[0]);await c.advance(400);assert.equal(a,1);assert.equal(b,1);assert.equal(first.getState().owed,false);assert.equal(second.getState().owed,true);assert.equal(second.getState().deferredUntil,7200400);assert.equal(section.snapshot('server/general'),before);
 hub.note(deps[0]);hub.setActive(false);hub.setActive(true);await c.advance(7199000);assert.equal(b,1);
 first.dispose();second.dispose();hub.dispose();section.dispose();assert.equal(c.pending(),0);
});

test('conflict waits for local settlement without busy retry and preserves all write-bearing proof',async()=>{
 const c=quietClock(),hub=new FreshnessHub(c.env);let remote={value:{name:'A',other:1},status:{revision:1}};
 const form=new DraftForm({load:async()=>remote,save:async()=>{}});await form.load();form.set({name:'B'});remote={value:{name:'C',other:2},status:{revision:2}};const before=form.getSnapshot();let reads=0;
 const input=mountQuietInput(hub,'settings/remote',deps,{id:'form',refresh:async()=>{reads++;return form.refresh();},subscribe:form.subscribe,blocked:()=>form.getSnapshot().saving,settlement:()=>form.getSnapshot().draft});
 input.invalidate();await c.advance(400);assert.equal(form.getSnapshot(),before);assert.equal(input.getState().owed,true);
 await c.advance(100000);assert.equal(reads,1);form.discard();await c.advance(400);assert.equal(reads,2);assert.equal(form.getSnapshot().status?.revision,2);assert.equal(input.getState().owed,false);
 input.dispose();form.dispose();hub.dispose();
});

test('write starts and finishes during a read: only one post-settlement read is gathered',async()=>{
 const c=quietClock(),hub=new FreshnessHub(c.env),held=deferred<{kind:'deferred'}>();let busy=false,serial=0,reads=0;const listeners=new Set<()=>void>();const notify=()=>{for(const listener of listeners)listener();};
 const input=mountQuietInput(hub,'settings/live',deps,{id:'config',refresh:async()=>++reads===1?held.promise:{kind:'adopted'},subscribe:fn=>{listeners.add(fn);return()=>{listeners.delete(fn);};},blocked:()=>busy,settlement:()=>serial});
 input.invalidate();await c.advance(400);busy=true;serial++;notify();busy=false;notify();held.resolve({kind:'deferred'});await tick();await c.advance(400);assert.equal(reads,2);assert.equal(input.getState().owed,false);await c.advance(60000);assert.equal(reads,2);
 input.setOpen(false);hub.note(deps[0]);await c.advance(1000);assert.equal(reads,2);input.dispose();hub.dispose();assert.equal(listeners.size,0);assert.equal(c.pending(),0);
});

test('section refresh bypasses memoized successful reads, keeps saveState, and carries exact errors',async()=>{
 let reads=0;const section=new SettingsSectionReads(()=>true,()=>{});await section.load('general',[{id:'form',load:async()=>{reads++;}}]);section.update({saveState:'saved'});const before=section.snapshot('general'),error=new Error('original');
 await section.load('general',[{id:'form',load:async()=>{reads++;}}]);assert.equal(reads,1);
 const result=await section.refresh('general',[{id:'form',refresh:async()=>{reads++;return {kind:'adopted'};}},{id:'second',refresh:async()=>({kind:'unreadable',error})}]);assert.equal(reads,2);assert.equal(result.kind,'unreadable');if(result.kind==='unreadable')assert.equal(result.error,error);assert.equal(section.snapshot('general'),before);section.dispose();
 assert.deepEqual(quietInterestOutcome({kind:'unreadable',error}),{kind:'failed',error,retryable:true});assert.deepEqual(quietInterestOutcome({kind:'deferred',nextAllowedAt:99999}),{kind:'deferred',nextAllowedAt:99999});
});


test('covered compact Settings closes every borrowed interest, cancels adoption and retains debt for reopen',async()=>{
 const c=quietClock(),hub=new FreshnessHub(c.env),visibility=createSettingsOpen(),held=deferred<{value:{name:string}}>();let reads=0;
 const form=new DraftForm({load:async()=>++reads===2?held.promise:{value:{name:reads===1?'A':'C'}},save:async()=>{}});await form.load();const before=form.getSnapshot();
 const input=mountQuietInput(hub,'settings/profile',deps,{id:'form',refresh:form.refresh.bind(form),cancel:form.invalidateQuiet},visibility);
 input.invalidate();await c.advance(400);visibility.setOpen(false);held.resolve({value:{name:'stale'}});await tick();assert.equal(form.getSnapshot(),before);assert.equal(input.getState().owed,true);await c.advance(60000);assert.equal(reads,2);
 visibility.setOpen(true);await c.advance(400);assert.equal(reads,3);assert.equal(form.getSnapshot().saved?.name,'C');input.dispose();form.dispose();hub.dispose();assert.equal(c.pending(),0);
});

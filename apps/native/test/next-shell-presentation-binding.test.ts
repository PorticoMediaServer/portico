import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeShellNavigation,type NativeRoutePresentation} from '../src-next/shell/routes';
const authority={serverId:'server',accountId:'account',profileId:'profile',epoch:'epoch',viewerFence:'viewer'};
function fixture(){
 let live=true;const listeners=new Set<()=>void>();const navigation=new NativeShellNavigation(authority,()=>authority);
 const owner={current:()=>live,subscribe:(listener:()=>void)=>{listeners.add(listener);return()=>{listeners.delete(listener);};}};
 return {navigation,owner,retire(){live=false;for(const listener of [...listeners])listener();},revive(){live=true;},listeners};
}
const top=(navigation:NativeShellNavigation)=>navigation.presentationStack().at(-1)!;
test('only the original owner-issued projection binds; equal route objects have independent occurrences',()=>{
 const f=fixture(),route={kind:'account' as const};f.navigation.open(route,authority);const first=top(f.navigation);
 assert.equal(f.navigation.bindPresentation({...first},f.owner),undefined);
 const a=f.navigation.bindPresentation(first,f.owner)!;assert(a);assert.equal(a.activeCurrent(),true);
 f.navigation.open(route,authority);const second=top(f.navigation),b=f.navigation.bindPresentation(second,f.owner)!;
 assert.notEqual(first.occurrence,second.occurrence);assert.equal(a.retainedCurrent(),true);assert.equal(a.activeCurrent(),false);assert.equal(b.activeCurrent(),true);
 f.navigation.back(authority);assert.equal(a.activeCurrent(),true);assert.equal(b.retainedCurrent(),false);
 f.navigation.open(route,authority);assert.equal(b.retainedCurrent(),false);a.dispose();b.dispose();f.navigation.dispose();
});
test('same-occurrence descriptor replacement retires the old resource; top/revision changes do not',()=>{
 const f=fixture(),root=f.navigation.bindPresentation(top(f.navigation),f.owner)!;f.navigation.top({kind:'home'},authority);assert.equal(root.retainedCurrent(),true);
 f.navigation.open({kind:'channel',sourceId:'source',channelId:'one'},authority);const original=top(f.navigation),binding=f.navigation.bindPresentation(original,f.owner)!;
 f.navigation.replaceChannel(f.navigation.getSnapshot(),{kind:'channel',sourceId:'source',channelId:'two'},authority);assert.equal(binding.retainedCurrent(),false);
 assert.equal(f.navigation.bindPresentation(original,f.owner),undefined);root.dispose();binding.dispose();f.navigation.dispose();
});
test('authority and disposal notifications invalidate monotonically and unsubscribe the original lease',()=>{
 const f=fixture(),binding=f.navigation.bindPresentation(top(f.navigation),f.owner)!;let notices=0;binding.subscribe(()=>{notices++;});
 f.retire();assert.equal(notices,1);assert.equal(binding.retainedCurrent(),false);assert.equal(f.listeners.size,0);f.revive();assert.equal(binding.activeCurrent(),false);
 const g=fixture(),other=g.navigation.bindPresentation(top(g.navigation),g.owner)!;let disposed=0;other.subscribe(()=>{disposed++;});g.navigation.dispose();assert.equal(disposed,1);assert.equal(other.retainedCurrent(),false);assert.equal(g.listeners.size,0);f.navigation.dispose();
});
test('retained/active notifications never make old revision commands fresh or bypass original route admission',()=>{
 let allowed=true;const f=fixture(),nav=new NativeShellNavigation(authority,()=>authority,()=>allowed);nav.open({kind:'account'},authority);
 const original=top(nav),snapshot=nav.getSnapshot(),binding=nav.bindPresentation(original,f.owner)!;let notices=0;binding.subscribe(()=>{notices++;});
 nav.open({kind:'servers'},authority);assert.equal(notices,1);nav.back(authority);assert.equal(notices,2);assert.equal(binding.activeCurrent(),true);
 assert.equal(nav.commitNativePop(snapshot,original.occurrence),false,'resource restoration never grants an obsolete command');
 allowed=false;assert.equal(binding.retainedCurrent(),false);allowed=true;assert.equal(binding.retainedCurrent(),false);nav.dispose();f.navigation.dispose();
});


test('a predicate that disposes the binding cannot return a current resource or active proof',()=>{
 for(const predicate of ['authority','admission'] as const){
  let dispose:()=>void=()=>{},armed=false,notices=0;
  const authorityListeners=new Set<()=>void>();
  const nav=new NativeShellNavigation(authority,()=>authority,()=>{if(armed&&predicate==='admission')dispose();return true;});
  const binding=nav.bindPresentation(top(nav),{current:()=>{if(armed&&predicate==='authority')dispose();return true;},subscribe:listener=>{authorityListeners.add(listener);return()=>{authorityListeners.delete(listener);};}})!;
  dispose=binding.dispose;binding.subscribe(()=>{notices++;assert.equal(binding.retainedCurrent(),false);});armed=true;
  assert.equal(binding.retainedCurrent(),false);assert.equal(binding.activeCurrent(),false);assert.equal(notices,1);assert.equal(authorityListeners.size,0);binding.dispose();assert.equal(notices,1);nav.dispose();
 }
});
test('a getter-discovered retirement notifies every original subscriber exactly once without recursive fanout',()=>{
 let live=true;const f=fixture();const binding=f.navigation.bindPresentation(top(f.navigation),{...f.owner,current:()=>live})!;
 let first=0,second=0;binding.subscribe(()=>{first++;assert.equal(binding.activeCurrent(),false);binding.dispose();});binding.subscribe(()=>{second++;});
 live=false;assert.equal(binding.retainedCurrent(),false);assert.equal(first,1);assert.equal(second,1);assert.equal(f.listeners.size,0);assert.equal(binding.retainedCurrent(),false);assert.equal(first,1);f.navigation.dispose();
});

import test from 'node:test';
import assert from 'node:assert/strict';
import {harness} from './shell-facts-fixture.ts';

function deferred<T>() {let resolve!:(value:T)=>void;const promise=new Promise<T>(done=>{resolve=done;});return {promise,resolve};}
test('resync refreshes changed display in place and leaves the admitted authority intact', async () => {
  const h=harness();h.rename();h.hub.resync();h.owner.all();await h.time.advance(1500);
  assert.equal(h.counts().retired,0);assert.equal(h.owner.source.getSnapshot().serverName,'Den');
  assert.equal(h.owner.source.getSnapshot().identity.libraries[0].name,'Cinema');
  h.dispose();assert.equal(h.time.pending(),0);
});
test('resync during an active read keeps one gathered follow-up instead of losing the final observation', async () => {
  const h=harness(),first=deferred<unknown>();h.libraryRead(()=>first.promise);
  h.owner.request('directory');await h.time.advance(400);assert.equal(h.calls.filter(path=>path==='/v1/libraries').length,1);
  for(let i=0;i<10;i++){h.hub.resync();h.owner.all();}
  h.rename();h.libraryRead();first.resolve({items:[{id:'library',name:'old answer',kind:'movies'}]});await h.time.advance(1500);
  assert.equal(h.calls.filter(path=>path==='/v1/libraries').length,2);
  assert.equal(h.owner.source.getSnapshot().identity.libraries[0].name,'Cinema');assert.equal(h.counts().retired,0);h.dispose();
});
test('failed resync keeps confirmed display and owed work until a later successful read', async () => {
  const h=harness(),error=new Error('offline');h.libraryRead(async()=>{throw error;});h.hub.resync();h.owner.all();await h.time.advance(1000);
  assert.equal(h.owner.source.getSnapshot().identity.libraries[0].name,'Films');assert.equal(h.counts().retired,0);
  h.rename();h.libraryRead();await h.time.advance(2500);
  assert.equal(h.owner.source.getSnapshot().identity.libraries[0].name,'Cinema');assert.equal(h.counts().retired,0);h.dispose();
});
test('disposing during resync prevents late publication and leaves no scheduled work', async () => {
  const h=harness(),gate=deferred<unknown>();h.libraryRead(()=>gate.promise);h.hub.resync();h.owner.all();await h.time.advance(400);
  const before=h.owner.source.getSnapshot();h.dispose();gate.resolve({items:[{id:'library',name:'Late',kind:'movies'}]});await h.time.advance(10000);
  assert.equal(h.owner.source.getSnapshot(),before);assert.equal(h.time.pending(),0);assert.equal(h.counts().retired,0);
});

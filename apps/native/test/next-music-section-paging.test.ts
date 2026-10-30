import test from 'node:test';
import assert from 'node:assert/strict';
import {MusicSectionPager} from '../src-next/screens/destinations/music/paging.ts';

test('music cursor bookmarks remain bounded while previous windows retain a genuine path back to the first page',async()=>{
 const reads:string[]=[];
 const pager=new MusicSectionPager<{id:string}>({current:()=>true,publish:()=>{},read:async cursor=>{
  reads.push(cursor);const offset=cursor?Number(cursor):0;
  return {items:[{id:String(offset)}],nextCursor:offset<80?String(offset+1):'',total:81,revision:'real',offset};
 }});
 await pager.load();while(pager.getSnapshot().canNext)await pager.next(pager.getSnapshot());
 assert.equal(reads.length,81);assert.equal(pager.getSnapshot().items.length,3);
 let previous=0;while(pager.getSnapshot().canPrevious){await pager.previous(pager.getSnapshot());assert(pager.getSnapshot().items.length<=3);assert(++previous<=81);}
 assert.equal(pager.getSnapshot().offset,0);assert.equal(pager.getSnapshot().items[0].id,'0');
 await pager.next(pager.getSnapshot());assert.equal(pager.getSnapshot().items.at(-1)!.id,'1');pager.dispose();
});

test('retained row identity survives appends; rows evicted from the bounded window are not retained in snapshots',async()=>{
 const pager=new MusicSectionPager<{id:string}>({current:()=>true,publish:()=>{},read:async cursor=>{const n=Number(cursor||0);return {items:[{id:String(n)}],nextCursor:n<5?String(n+1):'',offset:n,total:6,revision:'r'};}});
 await pager.load();const first=pager.getSnapshot().items[0];await pager.next(pager.getSnapshot());assert.equal(pager.getSnapshot().items[0],first);
 await pager.next(pager.getSnapshot());await pager.next(pager.getSnapshot());assert.equal(pager.getSnapshot().items.includes(first),false);pager.dispose();
});

test('scroll continuation cannot evict a focused original row, even if focus moves during the request',async()=>{
 let release:(()=>void)|undefined;let started:(()=>void)|undefined;
 const held=new Promise<void>(resolve=>{release=resolve;}),entered=new Promise<void>(resolve=>{started=resolve;});let calls=0;
 const pager=new MusicSectionPager<{id:string}>({current:()=>true,publish:()=>{},read:async cursor=>{const n=Number(cursor||0);calls++;if(n===3){started!();await held;}return {items:[{id:String(n)}],nextCursor:n<5?String(n+1):'',offset:n,total:6,revision:'r'};}});
 await pager.load();await pager.next(pager.getSnapshot());await pager.next(pager.getSnapshot());const original=pager.getSnapshot(),first=original.items[0];
 pager.focus(first,original);await pager.next(original);assert.equal(calls,3);assert.equal(pager.getSnapshot(),original,'real focused host is still offered');
 pager.blur(first,original);const next=pager.next(original);await entered;pager.focus(first,original);release!();await next;
 assert.equal(pager.getSnapshot().items[0],first,'late response cannot drop a host focused during IO');assert.equal(pager.getSnapshot().canNext,true);
 pager.blur(first,original);await pager.next(pager.getSnapshot());assert.equal(pager.getSnapshot().items.includes(first),false);pager.dispose();
});


test('a retry discarded for original focused-row retention restores the retained window navigation',async()=>{
 let fail=true,release!:()=>void,entered!:()=>void;let reads=0;
 const held=new Promise<void>(r=>{release=r;}),started=new Promise<void>(r=>{entered=r;});
 const pager=new MusicSectionPager<{id:string}>({current:()=>true,publish:()=>{},read:async cursor=>{
  const n=Number(cursor||0);reads++;
  if(n===3){if(fail)throw new TypeError('offline');entered();await held;}
  return {items:[{id:String(n)}],nextCursor:n<5?String(n+1):'',offset:n,total:6,revision:'r'};
 }});
 await pager.load();await pager.next(pager.getSnapshot());await pager.next(pager.getSnapshot());
 const original=pager.getSnapshot(),first=original.items[0];await pager.next(original);
 assert.equal(pager.getSnapshot().phase,'error');assert.equal(pager.getSnapshot().canNext,false);
 fail=false;const retry=pager.retry(pager.getSnapshot());await started;
 pager.focus(first,original);release();await retry;
 const retained=pager.getSnapshot();assert.equal(retained.phase,'ready');assert.equal(retained.items,original.items);
 assert.equal(retained.nextCursor,'3');assert.equal(retained.canNext,true);assert.equal(retained.canPrevious,false);
 await pager.retry(retained);assert.equal(reads,5);
 pager.blur(first,original);await pager.next(retained);assert.equal(reads,6);assert.equal(pager.getSnapshot().offset,1);pager.dispose();
});

test('only original issued section snapshots may focus or blur even when successor rows reuse the same immutable item',async()=>{
 const rows=Array.from({length:5},(_,n)=>Object.freeze({id:String(n)}));let reads=0;
 const make=()=>new MusicSectionPager<{id:string}>({current:()=>true,publish:()=>{},read:async cursor=>{
  reads++;const n=Number(cursor||0);return {items:[rows[n]],nextCursor:n<4?String(n+1):'',offset:n,total:5,revision:'r'};
 }});
 const old=make();await old.load();const oldSection=old.getSnapshot();old.dispose();
 const pager=make();await pager.load();const first=pager.getSnapshot();await pager.next(first);await pager.next(pager.getSnapshot());
 pager.focus(rows[0],oldSection);await pager.next(pager.getSnapshot());assert.equal(pager.getSnapshot().offset,1,'foreign generation cannot pin reused row');
 await pager.previous(pager.getSnapshot());const offered=pager.getSnapshot();pager.focus(rows[0],offered);
 pager.blur(rows[0],oldSection);const count=reads;await pager.next(offered);assert.equal(reads,count,'foreign blur cannot unpin current host');
 pager.blur(rows[0],offered);await pager.next(offered);assert.equal(reads,count+1);pager.dispose();
});

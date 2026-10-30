import test from 'node:test';
import assert from 'node:assert/strict';
import {createWindowedCollection,type FetchPage} from '../src/collections/windowed.ts';

const tick=()=>new Promise<void>(resolve=>setImmediate(resolve));
const page=(total:number,prefix='old'):FetchPage<string>=>async(start,count)=>({items:Array.from({length:Math.max(0,Math.min(count,total-start))},(_,i)=>`${prefix}${start+i}`),total});
const collection=()=>createWindowedCollection({fetchPage:page(100),pageSize:10,maxResidentPages:4,prefetchPages:0,keyOf:item=>item});

test('a quiet exact-zero read from a later page resolves and replaces the old window',async()=>{
 const list=collection();
 try{
  list.ensureRange(50,59);await tick();assert.equal(list.itemAt(50),'old50');
  const starts:number[]=[];let finished=false;
  const quiet=list.revalidate({fetchPage:async start=>{starts.push(start);return {items:[],total:0};}}).then(()=>{finished=true;});
  await tick();assert.equal(finished,true,'an authoritative empty response must settle without page zero');await quiet;
  assert.deepEqual(starts,[50]);assert.equal(list.getSnapshot().total,0);assert.equal(list.getSnapshot().status,'ready');assert.equal(list.getSnapshot().generation,1);
  assert.equal(list.itemAt(50),undefined);assert.equal(list.resident().items,0);
 }finally{list.dispose();}
});

test('an empty quiet read honors the input hold even if the viewport moves meanwhile',async()=>{
 const list=collection();
 try{
  list.ensureRange(50,59);await tick();
  let release!:()=>void;const hold=new Promise<void>(resolve=>{release=resolve;});let holding=0,finished=false;
  const starts:number[]=[];
  const quiet=list.revalidate({fetchPage:async start=>{starts.push(start);return {items:[],total:0};},hold:()=>{holding++;return hold;}}).then(()=>{finished=true;});
  await tick();assert.equal(holding,1);assert.equal(finished,false);assert.equal(list.itemAt(50),'old50');
  list.ensureRange(80,89);await tick();assert.deepEqual(starts,[50]);
  release();await tick();assert.equal(finished,true);await quiet;
  assert.equal(list.getSnapshot().total,0);assert.equal(list.itemAt(80),undefined);
 }finally{list.dispose();}
});

test('shrinking to a nonempty window still reads the real last page before publishing',async()=>{
 const list=collection();
 try{
  list.ensureRange(50,59);await tick();
  const starts:number[]=[];
  await list.revalidate({fetchPage:async(start,count,signal)=>{starts.push(start);return page(15,'new')(start,count,signal);}});
  assert.deepEqual(starts,[50,10]);assert.equal(list.getSnapshot().total,15);assert.equal(list.itemAt(14),'new14');
 }finally{list.dispose();}
});

test('page-zero empty results settle and lower-bound windows retain their uncertainty',async()=>{
 const list=collection();
 try{
  list.ensureRange(0,9);await tick();await list.revalidate({fetchPage:page(0)});
  assert.equal(list.getSnapshot().total,0);assert.equal(list.getSnapshot().totalIsLowerBound,false);
  await list.revalidate({fetchPage:async()=>({items:['next'],total:10,totalIsLowerBound:true})});
  assert.equal(list.getSnapshot().totalIsLowerBound,true);assert.equal(list.itemAt(0),'next');
 }finally{list.dispose();}
});

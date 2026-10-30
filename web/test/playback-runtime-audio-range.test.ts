import test from 'node:test';
import assert from 'node:assert/strict';
import {BLOCK,ByteCache,RangeReader} from '../src/platform/playback/audio/bytes.ts';

const reader=(fetcher:(url:string,init:RequestInit)=>Promise<Response>,options:{size?:number;limit?:number}={})=>new RangeReader({url:'https://media.example/audio',key:'audio',cache:new ByteCache(),fetch:fetcher,...options});

test('an injected fetch callback never receives the RangeReader as its receiver',async()=>{
 let calls=0;
 const fetcher=function(this:unknown,url:string,init:RequestInit):Promise<Response>{
  assert.equal(this,undefined);calls++;assert.equal(url,'https://media.example/audio');
  assert.equal((init.headers as Record<string,string>).Range,`bytes=0-${BLOCK-1}`);
  assert.equal(init.credentials,'omit');assert.equal(init.cache,'no-store');assert.equal(init.redirect,'error');assert.ok(init.signal);
  return Promise.resolve(new Response(new Uint8Array([1,2,3])));
 };
 const r=reader(fetcher);
 try{assert.deepEqual(await r.read(0,3),new Uint8Array([1,2,3]));assert.equal(calls,1);}finally{r.close();}
});

test('an ignored Range never buffers a whole unbounded file',async()=>{
 let cancelled=false, buffered=false;
 const response={status:200,ok:true,headers:new Headers({'Content-Length':String(BLOCK*20)}),body:{cancel:async()=>{cancelled=true;}},arrayBuffer:async()=>{buffered=true;return new ArrayBuffer(BLOCK*20);}} as unknown as Response;
 const r=reader(async()=>response);
 await assert.rejects(r.read(0,10));assert.equal(buffered,false);assert.equal(cancelled,true);r.close();
});

test('a wrong 206 range is refused rather than decoded as the requested position',async()=>{
 const r=reader(async()=>new Response(new Uint8Array(12),{status:206,headers:{'Content-Range':`bytes 0-11/${BLOCK*2}`}}));
 await assert.rejects(r.read(BLOCK,12));r.close();
});

test('aborting while a prepared reader waits for commit settles the read without fetching',async()=>{
 let calls=0;const r=reader(async()=>{calls++;throw new Error('not fetched');},{limit:0}),stop=new AbortController();
 const reading=r.read(0,12,stop.signal);stop.abort();
 const result=await Promise.race([reading.then(()=> 'resolved',()=> 'rejected'),new Promise<string>(resolve=>setTimeout(()=>resolve('hung'),25))]);
 r.close();await reading.catch(()=>{});assert.equal(result,'rejected');assert.equal(calls,0);
});

test('a streamed response enforces the actual block bound and cancels excess bytes',async()=>{
 let cancelled=false;
 const response={status:200,ok:true,headers:new Headers(),body:{getReader:()=>({read:async()=>({done:false,value:new Uint8Array(BLOCK+1)}),cancel:async()=>{cancelled=true;},releaseLock:()=>{}})},arrayBuffer:async()=>new ArrayBuffer(BLOCK+1)} as unknown as Response;
 const r=reader(async()=>response);await assert.rejects(r.read(0,12));assert.equal(cancelled,true);r.close();
});

test('valid ranges cross block boundaries and a small complete file can answer 200',async()=>{
 const source=Uint8Array.from({length:BLOCK+7},(_,i)=>i%251);let calls=0;
 const r=reader(async(_url,init)=>{
  calls++;const [,first,last]=/^bytes=(\d+)-(\d+)$/.exec((init.headers as Record<string,string>).Range)!;
  const a=Number(first),b=Math.min(Number(last),source.length-1);
  return new Response(source.slice(a,b+1),{status:206,headers:{'Content-Range':`bytes ${a}-${b}/${source.length}`}});
 },{size:source.length});
 try {assert.deepEqual(await r.read(BLOCK-3,10),source.slice(BLOCK-3));assert.equal(calls,2);assert.deepEqual(await r.read(BLOCK,7),source.slice(BLOCK));assert.equal(calls,2,'repeat reads use bounded cache');}finally{r.close();}
 const small=reader(async()=>new Response(new Uint8Array([1,2,3]),{status:200}));
 try {assert.deepEqual(await small.read(0,3),new Uint8Array([1,2,3]));}finally{small.close();}
});

test('cancellation interrupts a body that never answers and closed readers do not reuse cached bytes',async()=>{
 let cancelled=false;
 const response={status:200,ok:true,headers:new Headers(),body:{getReader:()=>({read:()=>new Promise(()=>{}),cancel:async()=>{cancelled=true;},releaseLock:()=>{}})}} as unknown as Response;
 const r=reader(async()=>response),stop=new AbortController();const reading=r.read(0,10,stop.signal);await new Promise(setImmediate);stop.abort();await assert.rejects(reading);assert.equal(cancelled,true);r.close();
 const cached=reader(async()=>new Response(new Uint8Array([1,2,3])));await cached.read(0,3);cached.close();await assert.rejects(cached.read(0,3));
});

test('each arriving chunk counts as progress before a slow block is complete',async()=>{
 let finish!:()=>void;const progress:number[]=[];
 const source=new ReadableStream<Uint8Array>({start(controller){controller.enqueue(new Uint8Array([1,2,3]));finish=()=>controller.close();}});
 const r=new RangeReader({url:'https://media.example/audio',key:'slow',cache:new ByteCache(),fetch:async()=>new Response(source),onBytes:n=>progress.push(n)});
 const reading=r.read(0,3);await new Promise(setImmediate);assert.deepEqual(progress,[3]);finish();assert.deepEqual(await reading,new Uint8Array([1,2,3]));r.close();
});

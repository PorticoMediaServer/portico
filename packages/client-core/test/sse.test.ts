import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readEventStream,type ServerEvent} from '../src/sse.ts';

const stream=(chunks:string[])=>new Response(new ReadableStream({start(c){const e=new TextEncoder();for(const x of chunks)c.enqueue(e.encode(x));c.close();}}));

test('frames are read across arbitrary chunk boundaries, with multi-line data, ids and comments',async()=>{
 const seen:ServerEvent[]=[];
 await readEventStream(stream([': hello\n\neve','nt: log\nda','ta: {"a":1}\nid: 7\n\n','data: one\ndata: two\n\r\n','event: heartbeat\n\n']),e=>seen.push(e));
 assert.deepEqual(seen.map(e=>[e.event,e.data,e.id]),[['log','{"a":1}','7'],['message','one\ntwo','7'],['heartbeat','','7']]);
});

test('a refused stream is an error with its status, and an abort ends the read quietly',async()=>{
 await assert.rejects(readEventStream(new Response('no',{status:403}),()=>{}),(e:any)=>e.status===403);
 const controller=new AbortController();let push:(s:string)=>void=()=>{};
 const open=new Response(new ReadableStream({start(c){const e=new TextEncoder();push=s=>c.enqueue(e.encode(s));}}));
 const seen:string[]=[];const done=readEventStream(open,e=>{seen.push(e.data);controller.abort();},controller.signal);
 push('data: first\n\n');await done;assert.deepEqual(seen,['first']);
});

test('an oversized frame is refused rather than buffered without bound',async()=>{
 await assert.rejects(readEventStream(stream(['data: '+'x'.repeat(300000)]),()=>{}),/oversized/);
});

test('a parse failure cancels the underlying open response before releasing its reader',async()=>{
 let cancelled=0;
 const response=new Response(new ReadableStream({start(c){c.enqueue(new TextEncoder().encode('data: '+'x'.repeat(300000)));},cancel(){cancelled++;}}));
 await assert.rejects(readEventStream(response,()=>{}),/oversized/);
 assert.equal(cancelled,1);assert.equal(response.body!.locked,false);
});

test('a frame of many small data lines that never ends is refused once it passes the cap',async()=>{
 // Each chunk is far below the cap and ends its line, so nothing waits unparsed between reads.
 const line='data: '+'x'.repeat(1000)+'\n';let sent=0,events=0;
 const endless=new Response(new ReadableStream({pull(c){if(sent++>1000){c.close();return;}c.enqueue(new TextEncoder().encode(line));}}));
 await assert.rejects(readEventStream(endless,()=>{events++;}),/oversized/);
 assert.ok(sent<300,'refused near the cap, not after everything arrived: '+sent);
 assert.equal(events,0);
});

test('one chunk carrying many complete small frames is read, however large the chunk',async()=>{
 const frames=4000,chunk=Array.from({length:frames},(_,i)=>'event: tick\ndata: '+String(i).padStart(90,'0')+'\n\n').join('');
 assert.ok(chunk.length>262144);
 const seen:string[]=[];
 await readEventStream(stream([chunk]),e=>seen.push(e.data));
 assert.equal(seen.length,frames);assert.equal(Number(seen[frames-1]),frames-1);
});

test('a single oversized line is refused even when it arrives whole with its line ending',async()=>{
 await assert.rejects(readEventStream(stream(['data: '+'x'.repeat(300000)+'\n\n']),()=>{}),/oversized/);
 await assert.rejects(readEventStream(stream(['event: '+'x'.repeat(300000)+'\ndata: 1\n\n']),()=>{}),/oversized/);
});

test('comment lines between frames never count toward a frame',async()=>{
 const seen:string[]=[];
 await readEventStream(stream([(': keepalive '+'k'.repeat(100)+'\n').repeat(4000),'data: after\n\n']),e=>seen.push(e.data));
 assert.deepEqual(seen,['after']);
});

test('the cap counts everything kept for a frame together: its event name, its data and the last id',async()=>{
 // Each field is under the cap by itself; together they are nearly three times it.
 const big='x'.repeat(100000);
 await assert.rejects(readEventStream(stream(['event: '+big+'\nid: '+big+'\ndata: '+big+'\n\n']),()=>{}),/oversized/);
 // The id outlives its frame, so it still counts against the next one.
 const seen:number[]=[];
 await assert.rejects(readEventStream(stream(['id: '+'i'.repeat(200000)+'\ndata: a\n\n','data: '+big+'\n\n']),e=>seen.push(e.data.length)),/oversized/);
 assert.deepEqual(seen,[1]);
});

test('a field that is sent again replaces what was kept, so repeating it does not add up',async()=>{
 const seen:ServerEvent[]=[];
 await readEventStream(stream([('event: '+'e'.repeat(100000)+'\n').repeat(8)+('id: '+'i'.repeat(100000)+'\n').repeat(8)+'data: kept\n\n']),e=>seen.push(e));
 assert.equal(seen.length,1);assert.equal(seen[0]!.data,'kept');assert.equal(seen[0]!.event.length,100000);
});

test('a CRLF line ending split between two reads is one line ending',async()=>{
 const seen:ServerEvent[]=[];
 await readEventStream(stream(['data: first\r','\ndata: second\r','\n\r','\n','event: next\r','data: third\r\r']),e=>seen.push(e));
 assert.deepEqual(seen.map(e=>[e.event,e.data]),[['message','first\nsecond'],['next','third']],'a lone CR still ends a line; only the LF completing a CRLF is dropped');
});

import {unreadableServerResponse} from '../src/server-messages.ts';
import test from 'node:test';
import assert from 'node:assert/strict';
import {validateSubtitleDocument, validateSubtitleDelivery, readSubtitleDocument} from '../src/subtitles.ts';

test('original delivery contracts retain grant paths, companion files and skipped cue facts',()=>{
 const base='/v1/media/subtitles/grant/sub/1';
 assert.equal(validateSubtitleDelivery({format:'ass',url:base+'/original.ssa',fontsUrl:base+'/fonts'}).fontsUrl,base+'/fonts');
 assert.equal(validateSubtitleDelivery({format:'vobsub',url:base+'/original.idx',companionUrl:base+'/original.sub'}).companionUrl,base+'/original.sub');
 for(const url of ['https://origin.invalid/sub.ass',base+'/../../private',base+'/original.exe'])assert.throws(()=>validateSubtitleDelivery({format:'ass',url}));
 const envelope={version:1,timeDomain:'source-relative',cues:[{startUs:'0',endUs:'1',text:'{LAUGHS}'}],skippedCues:2};
 const doc=validateSubtitleDocument(envelope);assert.equal(doc.skippedCues,2);assert.equal(doc.cues[0].text,'{LAUGHS}');
 assert.throws(()=>validateSubtitleDocument({...envelope,skippedCues:-1}));
});

 test('cue documents retain decoded literal text, overlaps and exact microsecond integers',()=>{
 const d=validateSubtitleDocument({version:1,timeDomain:'source-relative',cues:[{startUs:'1000000',endUs:'3000000',text:'2 < 3 & <b>literal</b>'},{startUs:'2000000',endUs:'2500000',text:'Overlapping\ncaption'}]});assert.equal(d.cues[0].text,'2 < 3 & <b>literal</b>');
 for(const cue of [{startUs:'01',endUs:'2',text:'x'},{startUs:'0',endUs:'0',text:'x'},{startUs:1,endUs:'2',text:'x'},{startUs:'0',endUs:'86400000001',text:'x'},{startUs:'0',endUs:'1',text:'\x00'}])assert.throws(()=>validateSubtitleDocument({version:1,timeDomain:'source-relative',cues:[cue]}));
 // Forgiving about cues: out of order is put in order, one bad cue costs only itself, styling that slipped through is removed.
 const mixed=validateSubtitleDocument({version:1,timeDomain:'source-relative',cues:[{startUs:'2',endUs:'4',text:'A'},{startUs:'1',endUs:'3',text:'{\\an8}B',top:true,italic:true},{startUs:'x',endUs:'3',text:'dropped'},{startUs:'5',endUs:'6',text:'\x00'},null,{startUs:'7',endUs:'9',text:'x'.repeat(9000)}]});
 assert.deepEqual(mixed.cues.map(c=>c.text.slice(0,4)),['B','A','xxxx']);assert.equal(mixed.cues[0].top,true);assert.equal(mixed.cues[0].italic,true);assert.equal(mixed.cues[1].top,undefined);assert.equal(mixed.cues[2].text.length,8192);
 // Strict about the envelope: a broken response is never shown as an empty track.
 for(const bad of [null,{version:2,timeDomain:'source-relative',cues:[]},{version:1,timeDomain:'wall-clock',cues:[]},{version:1,timeDomain:'source-relative',cues:'none'},{version:1,timeDomain:'source-relative',cues:new Array(20001).fill({startUs:'1',endUs:'2',text:'x'})}])assert.throws(()=>validateSubtitleDocument(bad));
 assert.equal(validateSubtitleDocument({version:1,timeDomain:'source-relative',cues:[]}).cues.length,0);
 });
 test('protected document reader validates MIME, declared size, UTF-8, status and cancellation',async()=>{
 const json=JSON.stringify({version:1,timeDomain:'source-relative',cues:[{startUs:'0',endUs:'1000000',text:'Hello'}]});const c=new AbortController();
 assert.equal((await readSubtitleDocument(new Response(json,{headers:{'Content-Type':'application/json'}}),c.signal)).cues.length,1);
 for(const response of [new Response(json),new Response(json,{status:403}),new Response(json,{headers:{'Content-Type':'application/json','Content-Length':'8388609'}}),new Response(new Uint8Array([0xff]),{headers:{'Content-Type':'application/json'}})])await assert.rejects(readSubtitleDocument(response,c.signal));
 c.abort();await assert.rejects(readSubtitleDocument(new Response(json,{headers:{'Content-Type':'application/json'}}),c.signal));
 });
test('the periodic subtitle reload sends its validator and treats 304 as authorised and unchanged',async()=>{
 const {fetchSubtitleDocument}=await import('../src/subtitles.ts');
 const body=JSON.stringify({version:1,timeDomain:'source-relative',cues:[{startUs:'0',endUs:'1000',text:'Hi'}]});
 const seen:RequestInit[]=[];
 const fetcher=(async(_url:string,init:RequestInit)=>{seen.push(init);const match=(init.headers as Record<string,string>|undefined)?.['If-None-Match'];return match==='"res.3"'?new Response(null,{status:304}):new Response(body,{status:200,headers:{'Content-Type':'application/json','ETag':'"res.3"'}});}) as unknown as typeof fetch;
 const first=await fetchSubtitleDocument(fetcher,'https://server.test/doc','',new AbortController().signal);
 assert.equal(first.unchanged,false);assert.equal((first as any).etag,'"res.3"');assert.equal(seen[0].headers,undefined);assert.equal(seen[0].cache,'no-store');
 const second=await fetchSubtitleDocument(fetcher,'https://server.test/doc','"res.3"',new AbortController().signal);
 assert.equal(second.unchanged,true);assert.equal(seen[1].cache,'no-store');
 // A validator that is not one is not sent; a revoked viewer's failure is a failure.
 await fetchSubtitleDocument(fetcher,'https://server.test/doc','not a validator\r\nX: y',new AbortController().signal);assert.equal(seen[2].headers,undefined);
 const revoked=(async()=>new Response('{}',{status:401,headers:{'Content-Type':'application/json'}})) as unknown as typeof fetch;
 await assert.rejects(fetchSubtitleDocument(revoked,'https://server.test/doc','"res.3"',new AbortController().signal));
});


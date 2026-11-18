import assert from 'node:assert/strict';
import test from 'node:test';
import {parseFeedbackReport} from '../src/notifications.ts';
import {consoleUTF8Size,readConsoleJson,ConsoleTransportError} from '../src/console-transport.ts';
test('UTF-8 bounds do not require native TextEncoder',()=>{for(const value of ['plain','caf\u00e9','\ud83c\udf7f','\ud800','\ud800X','\u4e2d\u6587'])assert.equal(consoleUTF8Size(value),Buffer.byteLength(value));});
function nativeResponse(value:string,status=200,length:string|null=String(Buffer.byteLength(value))):Response{
 const headers=new Headers({'Content-Type':'application/json'});if(length!==null)headers.set('Content-Length',length);
 return {ok:status>=200&&status<300,status,headers,body:null,text:async()=>value} as Response;
}
test('a refusal reads as unauthorized, except the management PIN, which keeps its code',async()=>{
 const signal=new AbortController().signal,body=(code:string)=>JSON.stringify({error:{code,message:'Refused.'}});
 await assert.rejects(readConsoleJson(nativeResponse(body('forbidden'),403),signal),(e:ConsoleTransportError)=>e.status===403&&e.code==='unauthorized');
 await assert.rejects(readConsoleJson(nativeResponse(body('profile_pin_manage'),401),signal),(e:ConsoleTransportError)=>e.status===401&&e.code==='unauthorized');
 await assert.rejects(readConsoleJson(nativeResponse(body('profile_pin_manage'),403),signal),(e:ConsoleTransportError)=>e.status===403&&e.code==='profile_pin_manage'&&/PIN/.test(e.message));
});
test('bounded native fetch accepts text without browser stream primitives',async()=>{assert.deepEqual(await readConsoleJson(nativeResponse('{"value":true}'),new AbortController().signal),{value:true});});
test('native fetch accepts a buffered body without a declared length but rejects deceptive and excessive bounds',async()=>{
 // A compressing proxy (chunked gzip) sends no Content-Length; native fetch has already buffered the body.
 const signal=new AbortController().signal;assert.deepEqual(await readConsoleJson(nativeResponse('{"ok":1}',200,null),signal),{ok:1});
 await assert.rejects(readConsoleJson(nativeResponse(' '.repeat(1048577),200,null),signal));
 await assert.rejects(readConsoleJson(nativeResponse('{}',200,'1048577'),signal));await assert.rejects(readConsoleJson(nativeResponse(' '.repeat(1048577),200,'2'),signal));
});
test('browser streaming enforces its real byte bound and abort state',async()=>{
 const signal=new AbortController().signal;assert.deepEqual(await readConsoleJson(new Response('{"ok":1}',{headers:{'Content-Type':'application/json'}}),signal),{ok:1});
 await assert.rejects(readConsoleJson(new Response(' '.repeat(1048577),{headers:{'Content-Type':'application/json'}}),signal));
 const aborted=new AbortController();aborted.abort();await assert.rejects(readConsoleJson(nativeResponse('{}'),aborted.signal));
});
test('server error text is never rendered, but validated field and revision hints survive',async()=>{
 try{await readConsoleJson(nativeResponse(JSON.stringify({error:{code:'console_conflict',message:'secret /private/token',currentRevision:5,fields:['values.name']}}),409),new AbortController().signal);assert.fail('must reject');}
 catch(e){assert.ok(e instanceof ConsoleTransportError);assert.equal(e.currentRevision,5);assert.deepEqual(e.fields,['values.name']);assert.ok(!e.message.includes('/private'));}
});
test('invalid media type, JSON and injected field names fail closed',async()=>{
 const signal=new AbortController().signal;await assert.rejects(readConsoleJson(new Response('<html/>',{headers:{'Content-Type':'text/html'}}),signal));await assert.rejects(readConsoleJson(nativeResponse('not JSON'),signal));
 try{await readConsoleJson(nativeResponse(JSON.stringify({error:{code:'invalid_console_request',fields:['secret/token\n']}}),400),signal);}catch(e){assert.ok(e instanceof ConsoleTransportError);assert.equal(e.fields,undefined);}
});
test('SIM-08: a feedback report names the app it came from, and an unknown online state stays unknown',()=>{
 const base={id:'report-one',cursor:'1',revision:1,status:'open',kind:'playback',category:'buffering',message:'Playback stopped.',createdAt:'2026-10-22T18:20:00Z',updatedAt:'2026-10-22T18:20:00Z',
  expiresAt:'2027-04-20T18:20:00Z',reporter:{name:'Alex',authority:'local',role:'member',self:false},diagnostics:{decision:'not-requested'},duplicates:0,thread:[]};
 assert.deepEqual(parseFeedbackReport({...base,client:{platform:'android',appVersion:'1.4.2',online:false}}).client,{platform:'android',appVersion:'1.4.2',online:false});
 assert.equal(parseFeedbackReport({...base,client:{platform:'web',appVersion:'2026.10'}}).client?.online,undefined);
 assert.equal(parseFeedbackReport(base).client,undefined);
 for(const client of [{platform:'',appVersion:'1'},{platform:'web',appVersion:''},{platform:'web',appVersion:'1',online:'yes'},{platform:'x'.repeat(41),appVersion:'1'},'web'])
  assert.throws(()=>parseFeedbackReport({...base,client}),JSON.stringify(client));
});
test('typed owner settings errors retain revision and bounded field paths',async()=>{
 const response=new Response(JSON.stringify({error:{code:'revision_mismatch',current:{revision:9},fields:[{path:'values.sessionStorageMiB',code:'invalid_value',message:'untrusted text'}]}}),{status:409,headers:{'Content-Type':'application/json'}});
 await assert.rejects(readConsoleJson(response,new AbortController().signal),e=>{
  assert.ok(e instanceof ConsoleTransportError);assert.equal(e.currentRevision,9);assert.deepEqual(e.fields,['values.sessionStorageMiB']);assert.doesNotMatch(e.message,/untrusted/);return true;
 });
});

test('a quiet form reload carries the owner\'s edit onto the newer document',async()=>{
 const {DraftForm}=await import('../src/server-admin/draft-form.ts');
 let remote={name:'Initial',limit:0};let sent:unknown;
 const form=new DraftForm({load:async()=>({value:remote,status:{revision:1}}),save:async(draft,saved)=>{sent={draft,saved};return {value:draft};}});
 await form.load();form.set({name:'My edit'});remote={name:'Initial',limit:123};await form.load();
 // What they did not touch shows the server's value; what they typed is still theirs, and still the only change.
 assert.deepEqual(form.getSnapshot().draft,{name:'My edit',limit:123});
 await form.save();
 assert.deepEqual(sent,{draft:{name:'My edit',limit:123},saved:{name:'Initial',limit:123}});
 form.dispose();
});

test('a save the server calls out of date is read again and sent again without a word',async()=>{
 const {DraftForm}=await import('../src/server-admin/draft-form.ts');
 for(const refusal of [{status:428,code:'revision_required'},{status:412,code:'revision_mismatch'},{status:409,code:'administration_conflict'}]){
  let remote={value:{name:'Initial',limit:0},status:{revision:1}};const sent:any[]=[];
  const form=new DraftForm<{name:string;limit:number},{revision:number}>({load:async()=>remote,save:async(draft,saved,status)=>{sent.push({draft,saved,revision:status?.revision});if(status?.revision!==remote.status.revision)throw Object.assign(new Error('moved'),refusal);remote={value:draft,status:{revision:remote.status.revision+1}};return remote;}});
  await form.load();form.set({name:'My edit'});
  // Somebody else changes another setting; this form has not heard yet.
  remote={value:{name:'Initial',limit:9},status:{revision:2}};
  await form.save();
  assert.deepEqual(sent.map(x=>x.revision),[1,2],refusal.code);
  assert.deepEqual(sent[1].draft,{name:'My edit',limit:9});
  const state=form.getSnapshot();
  assert.equal(state.dirty,false);assert.equal(state.saveError,undefined);assert.deepEqual(state.saved,{name:'My edit',limit:9});
  form.dispose();
 }
});

test('only a setting changed here and elsewhere is reported, and the newer document is kept under the draft',async()=>{
 const {DraftForm}=await import('../src/server-admin/draft-form.ts');
 let remote={value:{name:'Initial',limit:0},status:{revision:1}};let writes=0;
 const form=new DraftForm<{name:string;limit:number},{revision:number}>({load:async()=>remote,save:async(draft,_saved,status)=>{writes++;if(status?.revision!==remote.status.revision)throw Object.assign(new Error('moved'),{status:412,code:'revision_mismatch'});remote={value:draft,status:{revision:remote.status.revision+1}};return remote;}});
 await form.load();form.set({name:'My edit'});
 remote={value:{name:'Their edit',limit:5},status:{revision:2}};
 await assert.rejects(form.save(),{code:'revision_mismatch'});
 assert.equal(writes,1,'nothing is sent over a change the owner has not seen');
 let state=form.getSnapshot();
 assert.deepEqual(state.saved,{name:'Their edit',limit:5});assert.deepEqual(state.draft,{name:'My edit',limit:5});assert.equal(state.dirty,true);
 // Saving again is now a choice made knowing what changed.
 await form.save();
 state=form.getSnapshot();
 assert.equal(writes,2);assert.deepEqual(state.saved,{name:'My edit',limit:5});assert.equal(state.dirty,false);
 // The same value arriving from elsewhere is simply done.
 form.set({limit:7});remote={value:{name:'My edit',limit:7},status:{revision:4}};
 await form.save();
 assert.equal(writes,3,'one refused attempt, then nothing left to send');assert.equal(form.getSnapshot().dirty,false);
 form.dispose();
});

import test from 'node:test';
import assert from 'node:assert/strict';
import {SourceSettingsController} from '../src/screens/settings/server-livetv/source-settings-controller.ts';
const source={id:'lsr_source',name:'Cable',kind:'xtream',audience:'everyone',tunerCount:2,paused:false,state:'up_to_date',hasSignIn:true,config:{serverUrl:'https://provider.example',guideUrl:'https://guide.example',refreshHours:6}};
function fixture(){
 let current=true,revision=7,stored=source,fail=false;const calls:any[]=[];
 const api={async request<T>(path:string,method?:string,body?:unknown){calls.push({path,method,body});return (path.endsWith('/logos')?{requested:3,imported:2,skipped:1}:{values:{importImages:true}}) as T;},async requestRaw(path:string,method:string,options?:any){calls.push({path,method,...options});if(method==='PATCH'){if(fail)throw {status:503};stored={...stored,...options.body,hasSignIn:true};delete (stored as any).signIn;revision++;}return {status:200,headers:{etag:`"${revision}"`},body:stored};}};
 const controller=new SourceSettingsController(api,'server',source.id,()=>current,()=>{});
 return {controller,api,calls,stored:(value:any)=>{stored=value;revision++;},fail:()=>{fail=true;},retire:()=>{current=false;}};
}
test('source settings use the read ETag and real partial PATCH; credentials are blank after read and save',async()=>{
 const f=fixture();await f.controller.load();assert.equal(f.controller.getSnapshot().draft?.password,'');
 f.controller.edit({audience:'owner',username:'new-user',password:'new-password'});assert.equal(await f.controller.save(),true);
 const write=f.calls.find(call=>call.method==='PATCH');assert.equal(write.path,'/v1/admin/live/sources/lsr_source');assert.deepEqual(write.headers,{'If-Match':'"7"'});assert.deepEqual(write.body,{audience:'owner',signIn:{username:'new-user',password:'new-password'}});assert.equal(f.controller.getSnapshot().draft?.password,'');f.controller.dispose();
});
test('a save without an answer rereads before another edit, without replaying the request',async()=>{
 const f=fixture();await f.controller.load();f.controller.edit({name:'Changed'});f.fail();assert.equal(await f.controller.save(),false);assert.equal(f.calls.filter(call=>call.method==='PATCH').length,1);assert.equal(f.controller.getSnapshot().draft?.name,'Cable');assert.ok(f.controller.getSnapshot().error);f.controller.dispose();
});
test('retirement on loading or saving publication stops all subsequent I/O',async()=>{
 for(const flag of ['loading','saving'] as const){const f=fixture();if(flag==='saving'){await f.controller.load();f.controller.edit({name:'Changed'});}const count=f.calls.length;f.controller.subscribe(()=>{if(f.controller.getSnapshot()[flag])f.controller.dispose();});if(flag==='loading')await f.controller.load();else assert.equal(await f.controller.save(),false);assert.equal(f.calls.length,count);assert.equal(f.controller.getSnapshot().draft,undefined);}
});
test('quiet reads preserve a conflicting local edit and rebase disjoint changes',async()=>{
 const f=fixture();await f.controller.load();f.controller.edit({name:'Local'});const before=f.controller.getSnapshot();f.stored({...source,name:'Other'});assert.deepEqual(await f.controller.refreshQuietly(),{kind:'conflict'});assert.equal(f.controller.getSnapshot(),before);
 f.stored({...source,audience:'owner'});assert.deepEqual(await f.controller.refreshQuietly(),{kind:'adopted'});assert.equal(f.controller.getSnapshot().draft?.name,'Local');assert.equal(f.controller.getSnapshot().draft?.audience,'owner');f.controller.dispose();
});
test('private edits cannot silently rebase onto a different source revision',async()=>{
 const f=fixture();await f.controller.load();f.controller.edit({username:'private',password:'private'});f.stored({...source,name:'Other'});const before=f.controller.getSnapshot();assert.deepEqual(await f.controller.refreshQuietly(),{kind:'conflict'});assert.equal(f.controller.getSnapshot(),before);f.retire();assert.equal(f.controller.getSnapshot().draft,undefined);
});
test('manual logos use the rebuilt owner operation with an explicit replacement choice',async()=>{
 const f=fixture();await f.controller.load();await f.controller.importLogos(true);assert.deepEqual(f.calls.at(-1).body,{replaceExisting:true});assert.equal(f.calls.at(-1).path,'/v1/admin/live/sources/lsr_source/logos');assert.deepEqual(f.controller.getSnapshot().logoResult,{requested:3,imported:2,skipped:1});f.controller.dispose();
});
test('late upload text is discarded after the source panel closes',async()=>{
 const f=fixture();await f.controller.load();let complete!:(value:string)=>void;const pending=f.controller.upload('guide',()=>new Promise(done=>{complete=done;}));f.controller.dispose();complete('private-guide');await pending;assert.equal(f.controller.getSnapshot().draft,undefined);
});

test('a completed save fences an earlier quiet read, including its later settings read',async()=>{
 const f=fixture();await f.controller.load();const normal=f.api.requestRaw;let complete!:(value:any)=>void;
 f.api.requestRaw=async(path,method,options)=>method==='GET'?new Promise(done=>{complete=done;}):normal(path,method,options);
 const quiet=f.controller.refreshQuietly();f.controller.edit({name:'Saved'});assert.equal(await f.controller.save(),true);const before=f.controller.getSnapshot();complete({status:200,headers:{etag:'"99"'},body:{...source,name:'Stale'}});
 assert.deepEqual(await quiet,{kind:'deferred'});assert.equal(f.controller.getSnapshot(),before);assert.equal(f.calls.filter(call=>call.path==='/v1/admin/settings/live').length,1);f.controller.dispose();
});

test('an uncertain save stays locked if readback fails, while an explicit reload can recover it',async()=>{
 const f=fixture();await f.controller.load();const normal=f.api.requestRaw;let offline=false;
 f.api.requestRaw=async(path,method,options)=>{if(method==='PATCH'){offline=true;throw {status:503};}if(offline)throw {status:503};return normal(path,method,options);};
 f.controller.edit({name:'Pending'});assert.equal(await f.controller.save(),false);assert.equal(f.controller.getSnapshot().uncertain,true);f.controller.edit({name:'Cannot overtake'});assert.equal(f.controller.getSnapshot().draft?.name,'Pending');assert.equal(await f.controller.save(),false);
 offline=false;await f.controller.load();assert.ok(!f.controller.getSnapshot().uncertain);f.controller.edit({name:'Recovered'});assert.equal(f.controller.getSnapshot().draft?.name,'Recovered');f.controller.dispose();
});

test('group choices use the report and channel choices use source keys from the searched map',async()=>{
 const {SourceChoicesController}=await import('../src/screens/settings/server-livetv/source-choices-controller.ts');const calls:string[]=[];
 const api={async request<T>(path:string){calls.push(path);return (path.includes('/channels')?{items:[{id:'chn_news',sourceKey:'provider-news',name:'News'}]}:{...source,report:{groups:['News','Films']}}) as T;}};
 const groups=new SourceChoicesController(api,source.id,'groups',()=>true,()=>{}),channels=new SourceChoicesController(api,source.id,'channels',()=>true,()=>{});
 await groups.load('News');assert.deepEqual(groups.getSnapshot().page?.items,[{id:'News',name:'News'}]);await channels.load('News');assert.deepEqual(channels.getSnapshot().page?.items,[{id:'provider-news',name:'News'}]);assert.equal(calls[1],'/v1/admin/live/sources/lsr_source/channels?q=News');groups.dispose();channels.dispose();
});

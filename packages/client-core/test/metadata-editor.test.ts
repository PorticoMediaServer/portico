import test from 'node:test';
import assert from 'node:assert/strict';
import {MetadataEditorService,validateMetadataEdit,validMetadataPatch} from '../src/metadata-editor.ts';
const scope={serverId:'server',viewerId:'owner'},target={libraryId:'library',itemId:'item'};
const response=()=>({scope:{serverId:'server',libraryId:'library',itemId:'item',viewerFence:'a'.repeat(64)},revision:'b'.repeat(64),kind:'movie',title:{value:'Automatic',automaticValue:'Automatic',manual:false},description:{value:'',automaticValue:'',manual:false}});
const deferred=()=>{let resolve!:(value:any)=>void;return {promise:new Promise<any>(r=>resolve=r),resolve};};
test('manual edit projection whitelists nested scope and accepts source values above manual limits',()=>{
 const raw=response();raw.title.value=raw.title.automaticValue='t'.repeat(301);raw.description.value=raw.description.automaticValue='d'.repeat(20001);
 const parsed=validateMetadataEdit({...raw,scope:{...raw.scope,secret:'PRIVATE'}},scope,target);
 assert.equal(parsed.description.value.length,20001);assert.doesNotMatch(JSON.stringify(parsed),/PRIVATE|secret/);
 assert.equal(validMetadataPatch({title:'My title'}),true);assert.equal(validMetadataPatch({description:null}),true);
 assert.equal(validMetadataPatch({title:'x'.repeat(301)}),false);assert.equal(validMetadataPatch({description:'x'.repeat(20001)}),false);
 assert.equal(validMetadataPatch({title:undefined}),false);
 assert.throws(()=>validateMetadataEdit({...raw,title:{...raw.title,value:'t'.repeat(2049)}},scope,target));
 assert.throws(()=>validateMetadataEdit({...raw,description:{...raw.description,value:'d'.repeat(65537)}},scope,target));
});
test('a kind this app does not know and an optional date it cannot read are left off, not refused',()=>{
 const raw=response();
 const parsed=validateMetadataEdit({...raw,kind:'holo-sim',updatedAt:'not a date with PRIVATE'},scope,target);
 assert.equal(parsed.kind,undefined);assert.equal(parsed.updatedAt,undefined);
 assert.equal(parsed.title.value,raw.title.value);assert.equal(parsed.revision,raw.revision);
 assert.equal(validateMetadataEdit({...raw,updatedAt:'2026-09-24T02:00:00Z'},scope,target).updatedAt,'2026-09-24T02:00:00Z');
});
test('a projection for another server, library or item, or without a fence or revision, is refused',()=>{
 const raw=response();
 for(const patch of [
  {scope:{...raw.scope,serverId:'other'}},
  {scope:{...raw.scope,libraryId:'other'}},
  {scope:{...raw.scope,itemId:'other'}},
  {scope:{...raw.scope,viewerFence:'not-a-fence'}},
  {revision:'not-a-fence'},
 ])assert.throws(()=>validateMetadataEdit({...raw,...patch},scope,target));
});
test('title-only patch omits long untouched description and null releases long automatic backup',async()=>{
 let raw=response();raw.description.value=raw.description.automaticValue='d'.repeat(20001);const requests:any[]=[];
 const service=new MetadataEditorService({scope,api:{async request(_path,method,body){requests.push({method,body});if(method==='PATCH'){const patch=body as any;raw={...raw,revision:'c'.repeat(64),title:patch.title===null?{value:raw.title.automaticValue,automaticValue:raw.title.automaticValue,manual:false}:{value:patch.title,automaticValue:raw.title.automaticValue,manual:true}};}return raw as any;}}});
 await service.load(target);assert.equal(await service.save({title:'Owner'}),true);assert.deepEqual(requests[1].body,{expectedRevision:'b'.repeat(64),title:'Owner'});
 raw={...raw,revision:'d'.repeat(64),title:{...raw.title,automaticValue:'A'.repeat(301)}};await service.load(target);
 assert.equal(await service.save({title:null}),true);assert.equal(service.getSnapshot().data!.title.value.length,301);assert.equal(service.getSnapshot().data!.description.manual,false);
});
test('ignored-abort read settles at deadline and late response cannot overwrite a retry',async()=>{
 const pending=deferred();let calls=0;const service=new MetadataEditorService({scope,timeoutMs:5,api:{request(){return (++calls===1?pending.promise:Promise.resolve(response())) as any;}}});
 await service.load(target);assert.equal(service.getSnapshot().phase,'error');await service.load(target);const ready=service.getSnapshot();pending.resolve({...response(),revision:'c'.repeat(64)});await Promise.resolve();await Promise.resolve();assert.equal(service.getSnapshot(),ready);assert.equal(calls,2);
});
test('ignored-abort save becomes uncertain without replay; late commit requires explicit read',async()=>{
 const pending=deferred();let writes=0;let raw=response();const service=new MetadataEditorService({scope,timeoutMs:5,api:{request(_path,method){if(method==='PATCH'){writes++;return pending.promise;}return Promise.resolve(raw) as any;}}});
 await service.load(target);assert.equal(await service.save({title:'Owner'}),false);assert.equal(service.getSnapshot().phase,'uncertain');
 raw={...raw,title:{value:'Owner',automaticValue:'Automatic',manual:true}};pending.resolve(raw);await Promise.resolve();await Promise.resolve();assert.equal(service.getSnapshot().phase,'uncertain');assert.equal(writes,1);
 await service.load(target);assert.equal(service.getSnapshot().data!.title.value,'Owner');assert.equal(writes,1);
});
test('cancel and disposal settle ignoring-abort operations and fence late publication',async()=>{
 for(const dispose of [false,true]){const pending=deferred();const service=new MetadataEditorService({scope,api:{request:()=>pending.promise}});const load=service.load(target);if(dispose)service.dispose();else service.cancel();await load;pending.resolve(response());await Promise.resolve();assert.equal(service.getSnapshot().data,null);}
});
test('409 retains draft projection, denial clears data, and arbitrary server error text is omitted',async()=>{
 let failure:any;const service=new MetadataEditorService({scope,api:{async request(_path,method){if(method==='PATCH')throw failure;return response() as any;}}});
 await service.load(target);const before=service.getSnapshot().data;failure={status:409,message:'PRIVATE'};assert.equal(await service.save({title:'Draft'}),false);assert.equal(service.getSnapshot().phase,'conflict');assert.equal(service.getSnapshot().data,before);
 await service.load(target);failure={status:403,message:'PRIVATE'};await service.save({title:'Draft'});assert.equal(service.getSnapshot().phase,'denied');assert.equal(service.getSnapshot().data,null);assert.doesNotMatch(JSON.stringify(service.getSnapshot()),/PRIVATE/);
 const read=new MetadataEditorService({scope,api:{async request(){throw new Error('PRIVATE PATH');}}});await read.load(target);assert.doesNotMatch(JSON.stringify(read.getSnapshot()),/PRIVATE/);
});

import test from 'node:test';
import assert from 'node:assert/strict';
import {parsePersistedRemoteConfiguration} from '../src/linear-api.ts';
const remote={kind:'xtream',guideUrl:'https://guide.example/guide.xml',guideUrlRedacted:true,refreshMs:21600000,ownerLimit:4,useDiscoveredCapacity:false};
test('persisted acquisition projection reads every editable field without retaining private fields',()=>{
 const value=parsePersistedRemoteConfiguration({...remote,username:'secret-user',password:'secret-pass',futureSecret:'opaque'});
 assert.deepEqual(value,remote);assert(Object.isFrozen(value));assert(!JSON.stringify(value).includes('secret'));
});
test('persisted acquisition projection rejects malformed fields and policy bounds',()=>{
 for(const patch of [{refreshMs:0},{refreshMs:86400001},{ownerLimit:257},{ownerLimit:-1},{guideUrlRedacted:'yes'},{useDiscoveredCapacity:1},{guideUrl:'a\n'},{kind:'future'}])assert.throws(()=>parsePersistedRemoteConfiguration({...remote,...patch}));
});

test('persisted document client binds server, source and revision to each request',async()=>{
 const {liveSourceConfigurationService}=await import('../src/administration.ts');
 const filter={mode:'include',values:[]};
 const settings={enabled:true,viewerAccess:'owner-only',remote,enabledGroups:[],enabledChannels:[],kind:'playlist',streamBufferMs:null,retryWindowMs:null,userAgent:'',guideSourceId:'',importLimits:null,filters:{categories:filter,countries:filter,keywords:filter},logoImport:{enabled:true,overwriteExisting:false},channelNumbering:{mode:'source',startAt:1,step:1}};
 const result={sourceId:'source',sourceName:'Source',revision:3,digest:'digest',tunerCount:4,availableKinds:['playlist'],settings,effectiveLogoImport:{enabled:true,overwriteExisting:false},effective:{importLimits:{bytes:1,channels:1,programmes:1},streamBufferMs:8000,retryWindowMs:60000,userAgent:'',guideDays:7,logoImport:true,discoveryEnabled:true}};
 const calls:any[]=[];const client=liveSourceConfigurationService(async<T>(...args:any[])=>{calls.push(args);return {protocolVersion:'1.0',serverId:'server',result} as T;},'server','source');
 const doc=await client.read();await client.write(doc.revision,'operation',doc.settings);
 assert.equal(calls[0][0],'/v1/admin/live-sources/source/configuration');assert.equal(calls[1][1],'PUT');assert.equal(calls[1][2].expectedRevision,3);assert.equal(calls[1][2].settings.remote.password,undefined);
 const wrong=liveSourceConfigurationService(async<T>()=>({protocolVersion:'1.0',serverId:'server',result} as T),'server','different');await assert.rejects(wrong.read());
});

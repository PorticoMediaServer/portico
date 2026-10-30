import test from 'node:test';
import assert from 'node:assert/strict';
import {RemoteSourceService,sourceReadPreset,sourceReadPresetName} from '../src/remote-sources.ts';
import type {LibraryContentApi} from '../src/library-content.ts';
import type {SourceReadValues} from '../src/remote-sources.ts';

const scope={serverId:'server',viewerId:'owner'},wire={serverId:'server',viewerFence:'fence'};
const op='1788580000000-12345678-1234-1234-1234-123456789012';
const legacy={scanDepth:'basic',wholeFileReads:false,connections:4,requestsPerSecond:4,bytesPerSecond:0} as const;
const source=(generation=8)=>({id:'dav',kind:'webdav',name:'DAV',rootPath:'/server/remote/dav',generation,state:'ready',rangeSupport:'conditional',insecureLocal:false,credentialPresent:true,origin:'https://private.example'});
const profile={sourceId:'dav',generation:'7',revision:2,saved:true,values:legacy};
type Call={path:string;method:string;body:unknown};
function api(fn:(path:string,method:string,body:unknown)=>Promise<unknown>):LibraryContentApi{return {request:<T>(p:string,m='GET',b?:unknown)=>fn(p,m,b) as Promise<T>};}
function serviceFor(raw:(body?:unknown)=>unknown,calls:Call[]=[]){
 return new RemoteSourceService(api(async(path,method,body)=>{calls.push({path,method,body});return raw(body);}),scope,async()=>op);
}

test('GET keeps a present consent exact and leaves an older absence unknown',async()=>{
 for(const [consent,want] of [[true,true],[false,false]] as const){
  const service=serviceFor(()=>({scope:wire,settings:{sourceId:'dav',generation:'7',revision:2,saved:true,values:{...legacy,allowAdvancedMediaAnalysis:consent}}}));
  const values=(await service.readProfile('dav')).values;
  assert.equal(values.allowAdvancedMediaAnalysis,want);
  assert.equal(Object.hasOwn(values,'allowAdvancedMediaAnalysis'),true);
  service.dispose();
 }
 const older=serviceFor(()=>({scope:wire,settings:{sourceId:'dav',generation:'7',revision:2,saved:true,values:legacy}}));
 const values=(await older.readProfile('dav')).values;
 assert.equal(Object.hasOwn(values,'allowAdvancedMediaAnalysis'),false,'absence is unknown, never false');
 older.dispose();
});

test('a malformed present consent is refused instead of being dropped',async()=>{
 for(const consent of ['true',1,0,null,{},[],'false']){
  const service=serviceFor(()=>({scope:wire,settings:{sourceId:'dav',generation:'7',revision:2,saved:true,values:{...legacy,allowAdvancedMediaAnalysis:consent}}}));
  await assert.rejects(()=>service.readProfile('dav'),/Invalid remote source response/);
  service.dispose();
 }
});

test('every drawn preset omits consent and disables the legacy whole-file flag, and comparison ignores both',()=>{
 for(const name of ['list','once','everything','ask'] as const){
  const values=sourceReadPreset(name);
  assert.equal(Object.hasOwn(values,'allowAdvancedMediaAnalysis'),false,`${name} omits consent`);
  assert.equal(values.wholeFileReads,false,`${name} retires whole-file reads`);
 }
 const withConsent=(values:SourceReadValues,consent:boolean,wholeFile:boolean):SourceReadValues=>({...values,wholeFileReads:wholeFile,allowAdvancedMediaAnalysis:consent});
 assert.equal(sourceReadPresetName(withConsent(sourceReadPreset('everything'),true,true)),'everything');
 assert.equal(sourceReadPresetName(withConsent(sourceReadPreset('once'),false,true)),'once');
 assert.equal(sourceReadPresetName(withConsent(sourceReadPreset('list'),false,false)),'list');
 assert.equal(sourceReadPresetName(withConsent(sourceReadPreset('ask'),true,false)),'ask');
});

test('PUT copies a present consent and omits an unknown one',async()=>{
 const receipt=(body:unknown)=>({scope:wire,receipt:{operationId:op,accepted:true,removed:false,source:source(9),
  readSettings:{sourceId:'dav',generation:'9',revision:3,saved:true,values:(body as {values:SourceReadValues}).values}}});
 const calls:Call[]=[];
 const service=serviceFor(receipt,calls);
 const saved=await service.saveProfile(profile,{...legacy,allowAdvancedMediaAnalysis:true});
 assert.deepEqual((calls[0]!.body as {values:SourceReadValues}).values,{...legacy,allowAdvancedMediaAnalysis:true});
 assert.equal(saved.revision,3);
 service.dispose();
 const omitted:Call[]=[];
 const omitService=serviceFor(receipt,omitted);
 await omitService.saveProfile(profile,{...legacy});
 assert.equal(omitted.length,1);
 assert.equal(Object.hasOwn((omitted[0]!.body as {values:object}).values,'allowAdvancedMediaAnalysis'),false,'omission leaves the recorded consent alone');
 omitService.dispose();
});

test('a malformed consent is refused before any PUT',async()=>{
 const calls:Call[]=[];
 const service=serviceFor(()=>({scope:wire,receipt:{operationId:op,accepted:true,removed:false,source:source(9)}}),calls);
 await assert.rejects(()=>service.saveProfile(profile,{...legacy,allowAdvancedMediaAnalysis:'yes' as never}),/Invalid remote source response/);
 assert.equal(calls.length,0);
 service.dispose();
});

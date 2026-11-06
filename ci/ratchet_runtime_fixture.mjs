// Dependency mutations must agree with a fresh, uncached invocation of the
// existing counters. No files, installs or workspace links are created.
import assert from 'node:assert/strict';
import {createHash} from 'node:crypto';
import {fileURLToPath} from 'node:url';
import {dirname,resolve} from 'node:path';
import {TypedRatchet} from './typed_ratchet.mjs';
const root=resolve(dirname(fileURLToPath(import.meta.url)),'..');
const oid=text=>createHash('sha1').update('blob '+Buffer.byteLength(text)+'\0'+text).digest('hex');
const core='export declare class HttpLocalApi {consume(path: string, method?: string): Promise<unknown>;}';
const config={compilerOptions:{target:'ES2022',module:'ESNext',moduleResolution:'bundler',types:[],strict:true},include:['src/**/*']};
const local='export declare function makeClient(): {consume(name: string): void};';
const remote="import type {HttpLocalApi} from '../../packages/client-core/src/index'; export declare function makeClient(): HttpLocalApi;";
const initial=()=>({
 'packages/client-core/src/index.ts':core,
 'web/tsconfig.json':JSON.stringify(config),
 'web/src/caller.ts':"import {makeClient} from './barrel'; const api=makeClient();api.consume('/v1/items');",
 'web/src/barrel.ts':"export {makeClient} from './supplier';",
 'web/src/supplier.ts':local,
 'web/src/unrelated.ts':'export const untouched=true;'
});
let assertions=0;
for(const kind of ['census','query']) {
 const worker=new TypedRatchet(root,kind);let sources=initial();
 const request=()=>({root,kind,files:Object.keys(sources).filter(p=>p.startsWith('web/src/')&&p.endsWith('.ts')),configs:['web/tsconfig.json'],sources:Object.fromEntries(Object.entries(sources).map(([p,text])=>[p,{text,oid:oid(text)}]))});
 function check(expected,label) {
  const input=request(),result=worker.advance(input),fresh=new TypedRatchet(root,kind);
  try {
   const canonical=fresh.advance(input),payload=x=>kind==='census'?x.sites:x.reads;
   assert.deepEqual(payload(result),payload(canonical),kind+': '+label+' differs from uncached counter');
   const count=kind==='census'?result.sites.length:Object.values(result.reads).flat().length;
   assert.equal(count,expected,kind+': '+label);assertions+=2;return result;
  } finally {fresh.close();}
 }
 try {
  check(0,'local type through a barrel');
  sources['server/unloaded.ts']=local;check(0,'unloaded module exists');
  sources['server/unloaded.ts']=remote;
  check(0,'unloaded edit can reuse the unchanged program');
  assert.ok(worker.stats.unchangedPrograms>0);assertions++;
  sources['web/src/barrel.ts']="export {makeClient} from '../../server/unloaded';";
  // Fix the module's core import when it becomes loaded from another folder.
  sources['server/unloaded.ts']=remote.replace('../../packages','../packages');
  check(1,'previously ignored source becomes a resolved dependency');
  sources=initial();check(0,'restore initial dependency graph');
  const before=worker.stats.analysedFiles;
  sources['web/src/supplier.ts']=remote;
  check(1,'unchanged caller follows a transitive imported type');
  assert.ok(worker.stats.reusedFiles>=1,'unrelated source should be reused');assertions++;
  const analysed=worker.stats.analysedFiles;check(1,'identical frame');
  assert.equal(worker.stats.analysedFiles,analysed);assertions++;
  sources['web/src/barrel.ts']="export {makeClient} from './alternate';";
  sources['web/src/alternate.ts']=local;check(0,'barrel export changes provenance');
  sources['web/src/supplier.tsx']=remote;delete sources['web/src/supplier.ts'];
  sources['web/src/barrel.ts']="export {makeClient} from './supplier';";check(1,'replacement module extension');
  sources['web/src/supplier.ts']=local;check(0,'new module shadows an existing resolution');
  delete sources['web/src/supplier.ts'];check(1,'deletion restores the other resolution');
  sources['web/src/caller.ts']="import {makeClient} from '@source'; const api=makeClient();api.consume('/v1/items');";
  sources['web/tsconfig.json']=JSON.stringify({...config,compilerOptions:{...config.compilerOptions,baseUrl:'.',paths:{'@source':['src/alternate.ts']}}});
  // The census deliberately uses its existing synthetic project, independent
  // of app tsconfig aliases. The query counter uses the actual project options.
  if(kind==='query') {
   check(0,'changed tsconfig alias');
   sources['web/tsconfig.json']=JSON.stringify({...config,compilerOptions:{...config.compilerOptions,baseUrl:'.',paths:{'@source':['src/supplier.tsx']}}});
   check(1,'configuration changes an unchanged caller');
  }
  sources=initial();
  sources['web/src/caller.ts']="api.consume('/v1/items');";
  sources['web/src/ambient.d.ts']="import type {HttpLocalApi} from '../../packages/client-core/src/index'; declare global {var api: {consume(name: string): void};}";
  check(0,'global augmentation before');
  sources['web/src/ambient.d.ts']="import type {HttpLocalApi} from '../../packages/client-core/src/index'; declare global {var api: HttpLocalApi;}";
  check(1,'ambient augmentation changes an unchanged global caller');
  sources=initial();
  sources['package.json']=JSON.stringify({workspaces:['packages/*']});
  sources['packages/client-core/package.json']=JSON.stringify({name:'@portico/client-core',types:'src/index.ts'});
  sources['web/src/caller.ts']="import type {HttpLocalApi} from '@portico/client-core'; declare const api:HttpLocalApi; api.consume('/v1/items');";
  check(1,'workspace resolution without a physical node_modules link');
  assert.equal(worker.stats.projectOpens,1,'compiler projects remain open across all increments');assertions++;
 } finally {worker.close();}
}
console.log(JSON.stringify({assertions,result:'dependency parity holds'}));

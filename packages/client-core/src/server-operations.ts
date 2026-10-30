import {encodedRoutePath} from "../../contracts/src/route-paths.ts";
import type {ContentScope} from './library-content.ts';
import {noteSkipped} from './tolerant.ts';
export const operationPanels=['memory','database','build'] as const;
export type OperationPanelName=typeof operationPanels[number];
type Build={version:string|null;buildId:string|null;sourceDigest:string|null;builtAt:string|null;targetOS:string;targetArch:string;goVersion:string|null};
export type OperationPanel={name:OperationPanelName;observedAt:string;freshUntil:string;memory?:{heapObjectsBytes:number;runtimeReservedBytes:number};database?:{readable:true};build?:Build};
export interface OperationsApi {requestBounded<T>(path:string,maxBytes:number,signal:AbortSignal):Promise<T>}
export type OperationPanelState={phase:'idle'|'loading'|'ready'|'error'|'access-denied';panel:OperationPanel|null;error:string|null};
export type OperationsSnapshot=Readonly<Record<OperationPanelName,OperationPanelState>>;
const record=(v:unknown):v is Record<string,unknown>=>typeof v==='object'&&v!==null&&!Array.isArray(v);
const date=(v:unknown):v is string=>typeof v==='string'&&/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d{1,9})?Z$/.test(v)&&Number.isFinite(Date.parse(v));
const token=(v:unknown):v is string=>typeof v==='string'&&/^[A-Za-z0-9][A-Za-z0-9._+-]{0,127}$/.test(v);
const count=(v:unknown):v is number=>typeof v==='number'&&Number.isSafeInteger(v)&&v>=0;
function invalid():never {throw new Error('Invalid server observation.');}
export function parseOperationPanel(value:unknown,name:OperationPanelName):OperationPanel {
 if(!record(value)||value.name!==name||!date(value.observedAt)||!date(value.freshUntil)||Date.parse(value.freshUntil)-Date.parse(value.observedAt)!==30000)invalid();
 const base={name,observedAt:value.observedAt,freshUntil:value.freshUntil};
 if(name==='memory'){
  const m=value.memory;if(!record(m)||!count(m.heapObjectsBytes)||!count(m.runtimeReservedBytes)||m.heapObjectsBytes>m.runtimeReservedBytes)invalid();
  return {...base,memory:{heapObjectsBytes:m.heapObjectsBytes,runtimeReservedBytes:m.runtimeReservedBytes}};
 }
 if(name==='database'){
  if(!record(value.database)||value.database.readable!==true)invalid();
  return {...base,database:{readable:true}};
 }
 const b=value.build;if(!record(b))invalid();
 // A build detail this app cannot read is shown as not recorded. Only the panel that was asked for is ever read.
 const detail=(key:string,ok:(v:unknown)=>v is string):string|null=>{if(b[key]===null||ok(b[key]))return b[key] as string|null;noteSkipped('operations','build.'+key);return null;};
 if(!token(b.targetOS)||!token(b.targetArch))invalid();
 return {...base,build:{version:detail('version',token),buildId:detail('buildId',token),sourceDigest:detail('sourceDigest',token),builtAt:detail('builtAt',date),targetOS:b.targetOS,targetArch:b.targetArch,goVersion:detail('goVersion',token)}};
}
const empty=():OperationPanelState=>({phase:'idle',panel:null,error:null});
export class ServerOperationsService {
 private state:OperationsSnapshot={memory:empty(),database:empty(),build:empty()};
 private listeners=new Set<()=>void>();private disposed=false;private fenced=false;private fence?:string;
 private requests=new Map<OperationPanelName,AbortController>();
 private options:{api:OperationsApi;scope:ContentScope;timeoutMs?:number};
 constructor(options:{api:OperationsApi;scope:ContentScope;timeoutMs?:number}){
  if(!options.scope.serverId||!options.scope.viewerId)throw new Error('A selected server owner is required.');
  if(options.timeoutMs!==undefined&&(!Number.isFinite(options.timeoutMs)||options.timeoutMs<=0||options.timeoutMs>30000))throw new Error('Invalid observation timeout.');
  this.options={...options,scope:{...options.scope}};
 }
 getSnapshot=()=>this.state;
 subscribe=(fn:()=>void)=>{this.listeners.add(fn);return()=>{this.listeners.delete(fn);};};
 private publish(name:OperationPanelName,state:OperationPanelState){if(this.disposed)return;this.state={...this.state,[name]:state};for(const fn of this.listeners)fn();}
 private revoke(){this.fenced=true;for(const c of this.requests.values())c.abort();this.state={memory:empty(),database:empty(),build:empty()};for(const name of operationPanels)this.state={...this.state,[name]:{phase:'access-denied',panel:null,error:'Owner access changed. Reopen this server after signing in.'}};for(const fn of this.listeners)fn();}
 dispose(){this.disposed=true;for(const c of this.requests.values())c.abort();this.requests.clear();this.listeners.clear();}
 async refresh(name:OperationPanelName):Promise<void>{
  if(this.disposed||this.fenced||this.requests.has(name))return;
  const c=new AbortController();this.requests.set(name,c);const previous=this.state[name].panel;
  this.publish(name,{phase:'loading',panel:previous,error:null});let timer:ReturnType<typeof setTimeout>|undefined;
  try{
   const raw=await Promise.race([this.options.api.requestBounded<unknown>(encodedRoutePath("get_admin_operations_panel", {"panel": name}),8192,c.signal),new Promise<never>((_,reject)=>{timer=setTimeout(()=>{c.abort();reject(new Error('Observation timeout.'));},this.options.timeoutMs??7000);})]);
   if(this.disposed||this.fenced||c.signal.aborted)return;
   if(!record(raw)||!record(raw.scope)||raw.scope.serverId!==this.options.scope.serverId||typeof raw.scope.viewerFence!=='string'||!/^[a-f0-9]{64}$/.test(raw.scope.viewerFence)||this.fence&&this.fence!==raw.scope.viewerFence){this.revoke();return;}
   const panel=parseOperationPanel(raw.panel,name);this.fence=raw.scope.viewerFence;
   this.publish(name,{phase:'ready',panel,error:null});
  }catch(error){
   if(this.disposed||this.fenced)return;
   if(record(error)&&(error.status===401||error.status===403)){this.revoke();return;}
   this.publish(name,{phase:'error',panel:previous,error:'This observation could not be refreshed. Try again.'});
  }finally{if(timer)clearTimeout(timer);if(this.requests.get(name)===c)this.requests.delete(name);}
 }
}

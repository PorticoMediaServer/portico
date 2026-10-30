import {connectPairedServer, type ConnectionEnvironment} from '@core/server-connections.ts';
import type {HttpLocalApi} from '@core/index.ts';
import type {ServerPin} from '@core/route-identity.ts';
import {parseServerSignIn} from '@core/sign-in.ts';
import type {SignInKind} from '@core/app-sessions.ts';
import {routePath} from '../../../packages/contracts/src/routes.ts';

type StoragePort = Pick<Storage, 'getItem' | 'setItem'>;
export type ServerDisplaySource = Readonly<{pin:ServerPin;origin:string;signIn:SignInKind}>;
export type ServerDisplayChoice = Readonly<{id:string;label:string}>;
const validName=(value:unknown):value is string=>typeof value==='string'&&value.trim().length>0&&value.length<=120&&!/[\p{Cc}\p{Cf}]/u.test(value);
const key=(serverId:string)=>'portico.server-display.v1:'+serverId;

/** Public display metadata only. Names never authorize an account, select a
 * connection or replace a pin. An address is never a fallback server name. */
export class BrowserServerDisplay {
 private readonly names=new Map<string,string>();
 constructor(private readonly storage:StoragePort,private readonly environment:()=>ConnectionEnvironment,private readonly connect=connectPairedServer){}
 remember(api:HttpLocalApi,name:string){
  const route=api.getRouteConnection();if(!route||route.getSnapshot().phase!=='ready'||!validName(name))return;
  const id=route.pin.serverId;this.names.set(id,name);
  try{this.storage.setItem(key(id),JSON.stringify({version:1,name}));}catch{/* Display caching cannot fail a sign-in. */}
 }
 /** Synchronous display name for an already pinned server; never an address. */
 name(id:string):string|undefined{return this.read(id);}
 private read(id:string):string|undefined{
  const name=this.names.get(id);if(name)return name;
  try{const raw=this.storage.getItem(key(id));if(!raw||raw.length>1024)return;const value:unknown=JSON.parse(raw);if(value&&typeof value==='object'&&'version' in value&&value.version===1&&'name' in value&&validName(value.name)){this.names.set(id,value.name);return value.name;}}catch{/* Never substitute an origin for unavailable metadata. */}
 }
 async choices(sources:readonly ServerDisplaySource[],signal:AbortSignal):Promise<readonly ServerDisplayChoice[]>{
  const controller=new AbortController(),abort=()=>controller.abort(),timer=setTimeout(abort,2000);
  if(signal.aborted)abort();else signal.addEventListener('abort',abort,{once:true});let cursor=0;
  const worker=async()=>{
   while(!controller.signal.aborted&&cursor<sources.length){
    const source=sources[cursor++];if(this.read(source.pin.serverId))continue;
    let paired:Awaited<ReturnType<typeof connectPairedServer>>|undefined;
    try{
     paired=await this.connect(source.origin,source.pin,this.environment(),controller.signal);
     const system=parseServerSignIn(await paired.api.request(routePath('get_system'),'GET',undefined,controller.signal));
     if(controller.signal.aborted||system.id!==source.pin.serverId||system.signIn!==source.signIn)continue;
     this.remember(paired.api,system.name);
    }catch{/* Offline peers keep the last name actually read from this pinned server. */}
    finally{paired?.connection.dispose();}
   }
  };
  try{await Promise.all(Array.from({length:Math.min(4,sources.length)},worker));signal.throwIfAborted();
   return sources.flatMap(source=>{const label=this.read(source.pin.serverId);return label?[{id:source.pin.serverId,label}]:[];});
  }finally{clearTimeout(timer);signal.removeEventListener('abort',abort);controller.abort();}
 }
}
let instance:BrowserServerDisplay|undefined;
export function browserServerDisplay(){
 return instance??=new BrowserServerDisplay({getItem:key=>localStorage.getItem(key),setItem:(key,value)=>localStorage.setItem(key,value)},()=>({storage:{read:async()=>undefined,change:async()=>{throw new Error('Display metadata cannot write credentials.');}}}));
}

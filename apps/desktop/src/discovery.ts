import {invoke} from '@tauri-apps/api/core';
import {privateRouteHost} from '../../../packages/client-core/src/route-identity.ts';
import type {DiscoveryRoute} from '../../../packages/client-core/src/route-connection.ts';

type Bridge=Readonly<{browse(requestId:number):Promise<unknown>;cancel(requestId:number):Promise<void>}>;
const native:Bridge={browse:requestId=>invoke('desktop_discover',{requestId}),cancel:requestId=>invoke('desktop_discovery_cancel',{requestId})};
const unavailable=()=>new Error('discovery_unavailable');
const keys=['baseUrl','serverId','fingerprint','port','path','expiresAt'];
/** Native records are evidence for core's paired identity/proof checks only.
 * No service label, path, credential, pin or LAN/home grant is projected. */
export function desktopDiscoveryRows(value:unknown,now=Date.now()):readonly DiscoveryRoute[]{
  if(!Array.isArray(value)||value.length>64)throw unavailable();
  return Object.freeze(value.map(input=>{
    if(!input||typeof input!=='object'||Array.isArray(input)||Object.keys(input).length!==6||Object.keys(input).some(key=>!keys.includes(key)))throw unavailable();
    const v=input as Record<string,unknown>;
    if(typeof v.baseUrl!=='string'||v.baseUrl.length>256||typeof v.serverId!=='string'||!/^srv_[A-Za-z0-9_-]{43}$/.test(v.serverId)
      ||typeof v.fingerprint!=='string'||! /^[A-Za-z0-9_-]{43}$/.test(v.fingerprint)||v.path!=='/'
      ||typeof v.port!=='number'||!Number.isInteger(v.port)||v.port<1||v.port>65535
      ||typeof v.expiresAt!=='number'||!Number.isSafeInteger(v.expiresAt)||v.expiresAt<=now||v.expiresAt>now+1000)throw unavailable();
    let url:URL;try{url=new URL(v.baseUrl);}catch{throw unavailable();}
    if(url.protocol!=='https:'||url.username||url.password||url.search||url.hash||url.pathname!=='/'
      ||!/^\d+\.\d+\.\d+\.\d+$/.test(url.hostname)||!privateRouteHost(url.hostname)||url.hostname.startsWith('127.')
      ||Number(url.port||443)!==v.port)throw unavailable();
    return Object.freeze({baseUrl:url.origin,serverId:v.serverId,fingerprint:v.fingerprint,port:v.port,path:'/',expiresAt:v.expiresAt});
  }));
}
function requestId(){const bytes=crypto.getRandomValues(new Uint32Array(2));return bytes[0]!*2097152+(bytes[1]!>>>11)||1;}
/** Inert until core asks to browse. Abort/retirement cancels only this request;
 * late native facts never reach a replacement connection or browser fallback. */
export function createDesktopDiscovery(bridge:Bridge=native,clock=Date.now,nextId=requestId){
  const life=new AbortController();
  return Object.freeze({async discover(caller:AbortSignal):Promise<readonly DiscoveryRoute[]>{
    const signal=AbortSignal.any([caller,life.signal]);signal.throwIfAborted();
    const id=nextId();let abort!:()=>void;
    const cancelled=new Promise<never>((_,reject)=>{abort=()=>{
      void Promise.resolve().then(()=>bridge.cancel(id)).catch(()=>{});
      reject(new DOMException('Aborted','AbortError'));
    };});
    signal.addEventListener('abort',abort,{once:true});
    try{
      const result=await Promise.race([bridge.browse(id),cancelled]);signal.throwIfAborted();
      return desktopDiscoveryRows(result,clock());
    }catch{if(signal.aborted)throw new DOMException('Aborted','AbortError');throw unavailable();}
    finally{signal.removeEventListener('abort',abort);}
  },dispose(){life.abort();}});
}

import {NativeModules} from 'react-native';
import {encodedRoutePath} from '../../../../../../../packages/contracts/src/routes';
import {ConsoleClient} from '../../../../../../../packages/client-core/src/console';
import type {NativeShellBootstrapContext} from '../../../../shell/bootstrap';
import {NativeScreenLifetime,screenScopeEnded} from '../../lifetime';
import {nativeStorageOwner} from '../storage/space-source';
export type NativeLogoImage=Readonly<{name:'channel-logo.jpg';type:'image/jpeg';size:number;base64:string}>;
const object=(value:unknown):value is Record<string,unknown>=>!!value&&typeof value==='object'&&!Array.isArray(value);
/** The system picker returns normalized JPEG bytes, never a retained file URI. */
export async function pickNativeChannelLogo(isCurrent:()=>boolean):Promise<NativeLogoImage|undefined>{
  const picker:unknown=NativeModules.PorticoImagePicker;
  if(!isCurrent()||!object(picker)||typeof picker.pick!=='function')throw screenScopeEnded();
  const result:unknown=await picker.pick();
  if(!isCurrent())throw screenScopeEnded();if(result===null)return;
  if(!object(result)||result.contentType!=='image/jpeg'||typeof result.bytes!=='number'||!Number.isSafeInteger(result.bytes)||result.bytes<1||result.bytes>2*1024*1024||typeof result.base64!=='string'||result.base64.length!==4*Math.ceil(result.bytes/3)||!/^[A-Za-z0-9+/]*={0,2}$/.test(result.base64))throw new Error('invalid_channel_logo_image');
  const decoded=globalThis.atob(result.base64);if(decoded.length!==result.bytes||globalThis.btoa(decoded)!==result.base64)throw new Error('invalid_channel_logo_image');
  return Object.freeze({name:'channel-logo.jpg',type:'image/jpeg',size:result.bytes,base64:result.base64});
}
export async function nativeChannelLogoCapability(context:NativeShellBootstrapContext,lifetime:NativeScreenLifetime,isCurrent:()=>boolean):Promise<boolean>{
  const current=()=>lifetime.current()&&nativeStorageOwner(context)&&isCurrent();
  const api={request:<T,>(path:string,method?:string,body?:unknown,caller?:AbortSignal):Promise<T>=>current()?lifetime.request(signal=>context.api.request<T>(path,method,body,signal),caller):Promise.reject(screenScopeEnded())};
  const consoleClient=new ConsoleClient(api,{serverId:lifetime.authority.serverId,viewerId:lifetime.authority.profileId});
  try{if(!current())throw screenScopeEnded();const registry:unknown=await consoleClient.registry();if(!current())throw screenScopeEnded();if(!object(registry))throw new Error('invalid_channel_logo_capability');const option=registry.channelLogoUpload;if(option===undefined)return false;if(!object(option)||typeof option.base64Multipart!=='boolean'||Object.keys(option).some(key=>key!=='base64Multipart'))throw new Error('invalid_channel_logo_capability');return option.base64Multipart===true;}finally{consoleClient.dispose();}
}
/** Fixed registered owner route over the existing certificate-pinned native transport. */
export function nativeChannelLogoUpload(context:NativeShellBootstrapContext,lifetime:NativeScreenLifetime,isCurrent:()=>boolean){
  const captured=context.session();
  const current=()=>{const session=context.session();return !!captured&&!!session&&session.sessionFamilyId===captured.sessionFamilyId&&lifetime.current()&&nativeStorageOwner(context)&&isCurrent();};
  return async(channelId:string,image:NativeLogoImage,operationId:string,caller:AbortSignal):Promise<unknown>=>{
    if(!current()||caller.aborted)throw screenScopeEnded();
    if(!/^[A-Za-z0-9_-]{1,128}$/.test(channelId)||!/^[a-f0-9]{48}$/.test(operationId)||!Object.isFrozen(image)||image.type!=='image/jpeg'||image.name!=='channel-logo.jpg'||!Number.isSafeInteger(image.size)||image.size<1||image.size>2*1024*1024||image.base64.length!==4*Math.ceil(image.size/3)||!/^[A-Za-z0-9+/]*={0,2}$/.test(image.base64))throw new Error('invalid_channel_logo_image');
    const boundary='portico-'+operationId,path=encodedRoutePath('post_admin_library_channels_id_logo',{id:encodeURIComponent(channelId)});
    const body=`--${boundary}\r\nContent-Disposition: form-data; name="file"; filename="channel-logo.jpg"\r\nContent-Type: image/jpeg\r\nContent-Transfer-Encoding: base64\r\n\r\n${image.base64}\r\n--${boundary}\r\nContent-Disposition: form-data; name="operationId"\r\n\r\n${operationId}\r\n--${boundary}--\r\n`; // lint-strings-allow: exact MIME wire framing, never rendered UI copy.
    return lifetime.request(async signal=>{
      const session=context.session();if(!current()||signal.aborted||!session)throw screenScopeEnded();
      const response=await context.api.routeFetch(context.api.baseUrl+path,{method:'POST',body,signal,redirect:'error',credentials:'omit',referrerPolicy:'no-referrer',headers:{Authorization:'Bearer '+session.accessToken,Accept:'application/json','Content-Type':'multipart/form-data; boundary='+boundary}});
      if(!current()||signal.aborted){await response.body?.cancel();throw screenScopeEnded();}
      if(!response.ok){await response.body?.cancel();throw {status:response.status,code:response.status===401?'unauthorized':response.status===403?'not_permitted':'write_failed'};}
      const reader=response.body?.getReader();if(!reader)throw new Error('invalid_channel_logo_receipt');
      const chunks:Uint8Array[]=[];let size=0;
      try{for(;;){if(!current()||signal.aborted)throw screenScopeEnded();const part=await reader.read();if(part.done)break;size+=part.value.byteLength;if(size>32768)throw new Error('invalid_channel_logo_receipt');chunks.push(part.value);}if(!current()||signal.aborted)throw screenScopeEnded();const bytes=new Uint8Array(size);let offset=0;for(const chunk of chunks){bytes.set(chunk,offset);offset+=chunk.byteLength;}return JSON.parse(new TextDecoder('utf-8',{fatal:true}).decode(bytes));}finally{await reader.cancel();reader.releaseLock();}
    },caller);
  };
}

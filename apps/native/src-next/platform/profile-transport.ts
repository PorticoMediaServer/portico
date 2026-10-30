import {revisionRequiredFetch} from './revision-required-fetch';
import {NativeModules} from 'react-native';
import {HttpLocalApi} from '../../../../packages/client-core/src/index';
/** NSURLSession rejects redirects before sending passwords, PINs or profile proofs. */
export const nativeProfileFetch:typeof fetch=revisionRequiredFetch(async(input,options={})=>{
 if(typeof input!=='string'||options.signal?.aborted)throw new Error('Profile request cancelled.');
 const runtime=NativeModules.PorticoRuntime;if(typeof runtime.profileRequest!=='function')throw new Error('This build does not include the secure profile transport.');
 const id=runtime.playbackRequestId(),cancel=()=>runtime.cancelQueueRequest(id);options.signal?.addEventListener('abort',cancel,{once:true});
 try{const headers:Record<string,string>={};new Headers(options.headers).forEach((v,k)=>headers[k]=v);const raw=await runtime.profileRequest(id,input,options.method??'GET',headers,typeof options.body==='string'?options.body:null);if(options.signal?.aborted)throw new Error('Profile request cancelled.');return new Response(raw.status===204?null:raw.body,{status:raw.status,headers:raw.headers});}finally{options.signal?.removeEventListener('abort',cancel);}
});
export const profileAPI=(origin:string,token='')=>new HttpLocalApi(origin,token,nativeProfileFetch);

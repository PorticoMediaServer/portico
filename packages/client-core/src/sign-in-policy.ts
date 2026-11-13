import {serverPath, callServer} from './query/server-api.ts';
import type {SettingsConfiguration, ConfigurationValue} from './settings-configuration.ts';
import type {HttpLocalApi} from './index.ts';
import {CodedError, unreadableServerResponse} from './server-messages.ts';

/** Only policy settings the rebuilt server currently honours are published. */
export type SignInPolicyValues = Readonly<{staySignedInDays:number;pinRememberDays:number;requireTwoStep?:'nobody'|'administrators'|'everyone'}>;
type Property = keyof SignInPolicyValues;
export const signInPolicyRows: Readonly<Record<string, Property>> = Object.freeze({
 'signin.stayLength':'staySignedInDays','signin.pinRemember':'pinRememberDays','signin.requireTwoStep':'requireTwoStep',
});
export const isSignInPolicyRow = (id:string):boolean => Object.hasOwn(signInPolicyRows,id);
const object=(value:unknown):value is Record<string,unknown>=>!!value&&typeof value==='object'&&!Array.isArray(value);
const invalid=():never=>{throw new Error(unreadableServerResponse);};
function typed(key:Property,value:unknown):SignInPolicyValues[Property]|undefined{
 if(key==='requireTwoStep')return ['nobody','administrators','everyone'].includes(String(value))?value as SignInPolicyValues['requireTwoStep']:undefined;
 const n=typeof value==='string'&&value.trim()!==''?Number(value):value;
 return typeof n==='number'&&Number.isSafeInteger(n)&&(key==='staySignedInDays'?[7,30,90,365]:[0,7,30,90]).includes(n)?n:undefined;
}

/** The existing controller's configuration port, backed by actual settings and storage facts. */
export async function readOwnerSignInConfiguration(api:Pick<HttpLocalApi,'request'>,signal?:AbortSignal):Promise<SettingsConfiguration>{
 const [doc,storage]=await Promise.all([
  callServer(api,'GET /v1/admin/settings/{name}',{id:'sign-in',signal}),
  callServer(api,'GET /v1/admin/storage',{signal}),
 ]);
 if(doc.name!=='sign-in'||typeof doc.etag!=='string'||!/^"[0-9]+"$/.test(doc.etag)||!object(doc.values)||!object(doc.defaults)||typeof storage.dataFolder!=='string'||!storage.dataFolder)invalid();
 const revision=Number(doc.etag.slice(1,-1));if(!Number.isSafeInteger(revision))invalid();
 const values=doc.values,defaults=doc.defaults;
 if(!object(values)||!object(defaults))return invalid();
 const fields:ConfigurationValue[]=[];
 for(const [id,key] of Object.entries(signInPolicyRows)){
  if(doc.notApplicable?.[key])continue;
  const value=(v:unknown)=>typed(key,key==='requireTwoStep'&&v==='admins'?'administrators':v);
  const current=value(values[key]),original=value(defaults[key]);if(current===undefined||original===undefined)invalid();
  fields.push(Object.freeze({id,key:`signin.${key}`,current,effective:current,default:original,source:current===original?'default':'server',configOnly:false,applicationMode:'next-operation'}));
 }
 fields.push(Object.freeze({id:'signin.dataFolder',key:'signin.data_folder',current:storage.dataFolder,effective:storage.dataFolder,default:undefined,source:'file',configOnly:false,applicationMode:'startup'}));
 return Object.freeze({revision,registryRevision:doc.name,path:'',signInPolicyRevision:doc.etag,fields:Object.freeze(fields)});
}

export function signInPolicyValues(configuration:SettingsConfiguration|undefined):SignInPolicyValues|undefined{
 if(!configuration)return;
 const out:Partial<Record<Property,unknown>>={};
 for(const [id,key] of Object.entries(signInPolicyRows)){
  const field=configuration.fields.find(field=>field.id===id);
  if(!field&&key==='requireTwoStep')continue;
  const value=field&&typed(key,field.current);if(value===undefined)return;out[key]=value;
 }
 return Object.freeze(out) as SignInPolicyValues;
}
export function signInPolicyWith(values:SignInPolicyValues,rowId:string,value:unknown):SignInPolicyValues{
 const key=signInPolicyRows[rowId];if(!key)throw new Error('sign_in_policy_row_unknown');
 const next=typed(key,value);if(next===undefined)throw new Error('sign_in_policy_value_invalid');
 return Object.freeze({...values,[key]:next});
}
export type SignInPolicyAPI=Pick<HttpLocalApi,'request'>&Partial<Pick<HttpLocalApi,'requestRaw'>>;
/** The save carries the read ETag and never silently overwrites another device. */
export async function patchSignInPolicy(api:SignInPolicyAPI,expectedRevision:string,values:SignInPolicyValues,signal?:AbortSignal):Promise<unknown>{
 if(!api.requestRaw)throw new CodedError('unsupported','This app cannot save sign-in settings safely.');
 if(!/^"[0-9]+"$/.test(expectedRevision))invalid();
 const body={...values,...(values.requireTwoStep===undefined?{}:{requireTwoStep:values.requireTwoStep==='administrators'?'admins':values.requireTwoStep})};
 const result=await api.requestRaw(serverPath('PUT /v1/admin/settings/{name}',{id:'sign-in'}),'PUT',{headers:{'If-Match':expectedRevision},body,signal});
 if(result.status>=400){
  const error=object(result.body)&&object(result.body.error)?result.body.error:{};
  throw Object.assign(new Error(typeof error.message==='string'?error.message:unreadableServerResponse),{status:result.status,code:error.code});
 }
 return result.body;
}
export function signInPolicyConflict(error:unknown):boolean{
 return object(error)&&error.code==='changed_elsewhere';
}

import {callServer} from './query/server-api.ts';
import type {SettingsConfiguration, ConfigurationValue} from './settings-configuration.ts';
import type {HttpLocalApi} from './index.ts';
import {putSettingsDocument, settingsSaveConflict, type SettingsDocumentAPI} from './settings-document.ts';
import {unreadableServerResponse} from './server-messages.ts';

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

/** The existing controller's configuration port, backed by the rebuilt settings document. */
export async function readOwnerSignInConfiguration(api:Pick<HttpLocalApi,'request'>,signal?:AbortSignal):Promise<SettingsConfiguration>{
 const doc=await callServer(api,'GET /v1/admin/settings/{name}',{id:'sign-in',signal});
 if(doc.name!=='sign-in'||typeof doc.etag!=='string'||!/^"[0-9]+"$/.test(doc.etag)||!object(doc.values)||!object(doc.defaults))invalid();
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
export type SignInPolicyAPI=SettingsDocumentAPI;
/** The save carries the read ETag and never silently overwrites another device. */
export async function patchSignInPolicy(api:SignInPolicyAPI,expectedRevision:string,values:SignInPolicyValues,signal?:AbortSignal):Promise<unknown>{
 const body={...values,...(values.requireTwoStep===undefined?{}:{requireTwoStep:values.requireTwoStep==='administrators'?'admins':values.requireTwoStep})};
 return putSettingsDocument(api,'sign-in',expectedRevision,body,signal);
}
export const signInPolicyConflict = settingsSaveConflict;

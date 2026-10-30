import {routePath} from '../../contracts/src/route-paths.ts';
import {ApiAccessError, apiScopes,apiCredentialStates,type ApiCredentialState, type ApiAccessTransport, type ApiScope, type NewApiCredential} from './api-access.ts';

export type IntegrationAppPassword=Readonly<{id:string;name:string;username:string;accountId:string;profileId:string;scopes:readonly ApiScope[];state:ApiCredentialState}>;
export type IntegrationUser=Readonly<{accountId:string;profileId:string;profileName:string;username:string;role:string;disabled:boolean}>;
export type IntegrationPage<T>=Readonly<{items:readonly T[];nextCursor?:string}>;
export type NewIntegrationAppPassword=NewApiCredential&Readonly<{accountId:string;profileId:string}>;
export type IssuedIntegrationAppPassword=Readonly<{appPassword:IntegrationAppPassword;secret?:string}>;
const bad=():never=>{throw new ApiAccessError('invalid_document');};
const object=(v:unknown):Record<string,unknown>=>v!==null&&typeof v==='object'&&!Array.isArray(v)?v as Record<string,unknown>:bad();
const text=(v:unknown,max=128):string=>typeof v==='string'&&v.length>0&&v.length<=max?v:bad();
const optional=(v:unknown,max=512):string|undefined=>v===undefined||v===''?undefined:text(v,max);
const scopes=(v:unknown):readonly ApiScope[]=>Array.isArray(v)&&v.length>0&&v.length<=apiScopes.length?Object.freeze(v.map(scope=>apiScopes.includes(scope)?scope as ApiScope:bad())):bad();
export function parseIntegrationAppPassword(raw:unknown):IntegrationAppPassword {
 const v=object(raw);if('secret' in v||!apiCredentialStates.includes(v.state as ApiCredentialState))bad();
 return Object.freeze({id:text(v.id),name:text(v.name,80),username:text(v.username),accountId:text(v.accountId),profileId:text(v.profileId),scopes:scopes(v.scopes),state:v.state as ApiCredentialState});
}
function page<T>(raw:unknown,parse:(v:unknown)=>T):IntegrationPage<T>{const v=object(raw);if(!Array.isArray(v.items)||v.items.length>100)bad();return Object.freeze({items:Object.freeze((v.items as unknown[]).map(parse)),nextCursor:optional(v.nextCursor)});}
export function parseIntegrationUsers(raw:unknown):IntegrationPage<IntegrationUser>{return page(raw,raw=>{
 const v=object(raw);if(typeof v.disabled!=='boolean')bad();
 return Object.freeze({accountId:text(v.accountId),profileId:text(v.profileId),profileName:text(v.profileName),username:text(v.username),role:text(v.role,32),disabled:v.disabled as boolean});
});}
const query=(cursor:string)=>cursor?`?cursor=${encodeURIComponent(cursor)}`:'';
export const listIntegrationAppPasswords=async(api:ApiAccessTransport,cursor='',signal?:AbortSignal)=>page(await api.request<unknown>(routePath('list_integration_app_passwords')+query(cursor),'GET',undefined,signal),parseIntegrationAppPassword);
export const listIntegrationUsers=async(api:ApiAccessTransport,cursor='',signal?:AbortSignal)=>parseIntegrationUsers(await api.request<unknown>(routePath('integration_users')+query(cursor),'GET',undefined,signal));
export async function createIntegrationAppPassword(api:ApiAccessTransport,q:NewIntegrationAppPassword,signal?:AbortSignal):Promise<IssuedIntegrationAppPassword>{
 const v=object(await api.request<unknown>(routePath('create_integration_app_password'),'POST',{...q,scopes:[...q.scopes]},signal)),appPassword=parseIntegrationAppPassword(v.appPassword),secret=optional(v.secret);
 if(secret!==undefined&&!secret.startsWith('papp.'))bad();
 return Object.freeze({appPassword,secret});
}
export const revokeIntegrationAppPassword=async(api:ApiAccessTransport,id:string,signal?:AbortSignal)=>parseIntegrationAppPassword(await api.request<unknown>(routePath('revoke_integration_app_password',{id}),'DELETE',undefined,signal));
async function reveal(api:ApiAccessTransport,path:string,id:string,prefix:string,signal?:AbortSignal):Promise<string>{const v=object(await api.request<unknown>(path,'GET',undefined,signal));if(v.id!==id)bad();const secret=text(v.secret,512);if(!secret.startsWith(prefix))bad();return secret;}
export const revealApiKey=(api:ApiAccessTransport,id:string,signal?:AbortSignal)=>reveal(api,routePath('reveal_api_key',{id}),id,'pk.',signal);
export const revealIntegrationAppPassword=(api:ApiAccessTransport,id:string,signal?:AbortSignal)=>reveal(api,routePath('reveal_integration_app_password',{id}),id,'papp.',signal);

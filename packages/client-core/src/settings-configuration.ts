import {unreadableServerResponse} from './server-messages.ts';

export type ConfigurationValue = Readonly<{id:string;key:string;current:unknown;effective:unknown;default:unknown;source:'default'|'server'|'file';configOnly:boolean;applicationMode:string}>;
export type SettingsConfiguration = Readonly<{revision:number;registryRevision:string;path:string;fields:readonly ConfigurationValue[]}>;
export type SettingsConfigurationAPI = Readonly<{request<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal):Promise<T>}>;
const object=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==='object'&&!Array.isArray(v);
const text=(v:unknown,max:number):v is string=>typeof v==='string'&&v.length<=max&&!/[\u0000-\u001f]/.test(v);
const bad=():never=>{throw new Error(unreadableServerResponse);};
function value(v:unknown,depth=0):unknown{
 if(v===null||typeof v==='boolean'||typeof v==='string'&&text(v,4096)||typeof v==='number'&&Number.isFinite(v))return v;
 if(depth>2)return bad();
 if(Array.isArray(v)&&v.length<=256)return Object.freeze(v.map(v=>value(v,depth+1)));
 if(object(v)&&Object.keys(v).length<=32)return Object.freeze(Object.fromEntries(Object.entries(v).map(([k,v])=>[text(k,128)?k:bad(),value(v,depth+1)])));
 return bad();
}
export function parseSettingsConfiguration(raw:unknown):SettingsConfiguration{
 if(!object(raw)||!Number.isSafeInteger(raw.revision)||(raw.revision as number)<1||!text(raw.registryRevision,128)||!text(raw.path,4096)||!Array.isArray(raw.fields)||raw.fields.length>1024)return bad();
 const ids=new Set<string>();const keys=new Set<string>();
 const fields=raw.fields.map((field:unknown):ConfigurationValue=>{
  if(!object(field)||!text(field.id,160)||!field.id||ids.has(field.id)||!text(field.key,160)||!field.key||keys.has(field.key)||!['default','server','file'].includes(String(field.source))||typeof field.configOnly!=='boolean'||!text(field.applicationMode,64))return bad();
  for(const k of ['current','effective','default'])if(!(k in field))return bad();
  ids.add(field.id);keys.add(field.key);
  return Object.freeze({id:field.id,key:field.key,current:value(field.current),effective:value(field.effective),default:value(field.default),source:field.source as ConfigurationValue['source'],configOnly:field.configOnly,applicationMode:field.applicationMode});
 });
 return Object.freeze({revision:raw.revision as number,registryRevision:raw.registryRevision,path:raw.path,fields:Object.freeze(fields)});
}
/** Search uses current values from the producer, including config-only fields. */
export function searchConfiguration(configuration:SettingsConfiguration,query:string):readonly ConfigurationValue[]{
 const words=query.trim().toLocaleLowerCase().split(/\s+/);if(query.trim().length<2)return [];
 return configuration.fields.filter(f=>words.every(word=>`${f.id} ${f.key} ${String(f.current)} ${String(f.effective)} ${String(f.default)}`.toLocaleLowerCase().includes(word)));
}

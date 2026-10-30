import {unreadableServerResponse} from './server-messages.ts';
import {noteSkipped} from './tolerant.ts';
/** Viewer preference registry published by the selected server on
 * `GET /v1/preferences`. The server owns every default, domain and clamp; a
 * client renders settings from the registry it is handed and never invents a
 * field, a default or a limit of its own. */
export type PreferenceScope='profile-server'|'profile-device-class'|'profile-device';
export type DeviceClass='web'|'mobile'|'tablet'|'television';
/** `locale` is a string with a semantic domain; `languageList` is an ordered list of language
 * codes (the server's semantic types). There is no `timeZone` type: times are shown in the
 * device's zone, and a server that still publishes such a field has it left out like any other
 * type this client doesn't know. */
export type PreferenceType='boolean'|'integer'|'number'|'string'|'string-list'|'locale'|'languageList';
export type PreferenceValue=boolean|number|string|readonly string[];
export type PreferenceField=Readonly<{
 key:string;type:PreferenceType;default:PreferenceValue;allowedValues?:readonly (number|string)[];
 min?:number;max?:number;scopes:readonly PreferenceScope[];group:string;labelKey:string}>;
export type PreferenceRegistry=Readonly<{revision:string;fields:readonly PreferenceField[]}>;
export type PreferenceValues=Readonly<Record<string,PreferenceValue>>;
export type PreferenceDocument=Readonly<{scope:PreferenceScope;revision:number;digest:string;activeRevision:number;values:PreferenceValues}>;
export type PreferenceSnapshot=Readonly<{
 deviceClass:DeviceClass;deviceBound?:boolean;registry:PreferenceRegistry;documents:readonly PreferenceDocument[];
 effective:PreferenceValues;effectiveSource:Readonly<Record<string,string>>;
 clampedFields:readonly string[];registryRevision:string}>;
/** A patch carries only the keys it changes. `null` removes that scope's
 * override so the registry default becomes effective again. */
export type PreferencePatch=Readonly<Record<string,PreferenceValue|null>>;

const preferenceScopes:readonly PreferenceScope[]=['profile-server','profile-device-class','profile-device'];
const deviceClasses:readonly DeviceClass[]=['web','mobile','tablet','television'];
const preferenceTypes:readonly PreferenceType[]=['boolean','integer','number','string','string-list','locale','languageList'];
const object=(v:unknown):v is Record<string,unknown>=>typeof v==='object'&&v!==null&&!Array.isArray(v);
const counter=(v:unknown):v is number=>typeof v==='number'&&Number.isSafeInteger(v)&&v>=0;
const finite=(v:unknown):v is number=>typeof v==='number'&&Number.isFinite(v);
const text=(v:unknown,max:number):v is string=>typeof v==='string'&&v.length>0&&v.length<=max;
const key=(v:unknown):v is string=>typeof v==='string'&&/^[a-z][A-Za-z0-9]*(\.[a-zA-Z0-9][A-Za-z0-9-]*){1,3}$/.test(v);
function invalid():never{throw new Error(unreadableServerResponse);}

function parseValue(field:PreferenceField,value:unknown):PreferenceValue {
 switch(field.type){
  case 'boolean':if(typeof value!=='boolean')invalid();return value;
  case 'integer':if(!Number.isSafeInteger(value))invalid();return value as number;
  case 'number':if(!finite(value))invalid();return value;
  case 'string':case 'locale':if(typeof value!=='string'||value.length>4096)invalid();return value;
  case 'string-list':case 'languageList':{
   if(!Array.isArray(value)||value.length>200)invalid();
   const seen=new Set<string>();
   for(const entry of value){if(!text(entry,512)||seen.has(entry))invalid();seen.add(entry);}
   return Object.freeze([...value as string[]]);
  }
 }
 invalid();
}

/** A field of a type this client doesn't know is left out (tolerant reader): the rest of the
 * registry still renders, and values for it are ignored. A field whose definition cannot be
 * read throws here and is left out by the snapshot parser in the same way. */
function parseField(raw:unknown):PreferenceField|undefined {
 if(!object(raw)||!key(raw.key)||typeof raw.type!=='string')invalid();
 if(!preferenceTypes.includes(raw.type as PreferenceType))return undefined;
 if(!text(raw.group,64)||!text(raw.labelKey,160)||raw.labelKey!=='preferences.'+raw.key)invalid();
 if(!Array.isArray(raw.scopes)||raw.scopes.length<1||raw.scopes.length>preferenceScopes.length)invalid();
 const scopes=new Set<PreferenceScope>();
 for(const scope of raw.scopes){if(!preferenceScopes.includes(scope as PreferenceScope)||scopes.has(scope as PreferenceScope))invalid();scopes.add(scope as PreferenceScope);}
 for(const bound of ['min','max'] as const)if(raw[bound]!==undefined&&!finite(raw[bound]))invalid();
 if(finite(raw.min)&&finite(raw.max)&&raw.min>raw.max)invalid();
 let allowed:readonly (number|string)[]|undefined;
 if(raw.allowedValues!==undefined){
  if(!Array.isArray(raw.allowedValues)||raw.allowedValues.length<1||raw.allowedValues.length>64)invalid();
  // An option is a bare value, or the server's labelled option {value, label, …} (quality presets).
  const values=raw.allowedValues.map(entry=>object(entry)?entry.value:entry);
  for(const entry of values)if(!(typeof entry==='string'&&entry.length>0&&entry.length<=160)&&!finite(entry))invalid();
  allowed=Object.freeze([...values as (number|string)[]]);
 }
 const field:PreferenceField=Object.freeze({
  key:raw.key,type:raw.type as PreferenceType,default:undefined as unknown as PreferenceValue,
  ...(allowed?{allowedValues:allowed}:{}),...(finite(raw.min)?{min:raw.min}:{}),...(finite(raw.max)?{max:raw.max}:{}),
  scopes:Object.freeze([...scopes]),group:raw.group,labelKey:raw.labelKey});
 // The default has to satisfy the field's own published domain, or the registry
 // is describing something the server would refuse on the way back in.
 const value=parseValue(field,raw.default);
 if(allowed&&typeof value!=='object'&&!allowed.includes(value as number|string))invalid();
 return Object.freeze({...field,default:value});
}

function parseValues(fields:ReadonlyMap<string,PreferenceField>,raw:unknown,scope?:PreferenceScope,unknown:ReadonlySet<string>=new Set()):PreferenceValues {
 if(!object(raw))invalid();
 const out:Record<string,PreferenceValue>={};
 for(const [name,value] of Object.entries(raw)){
  if(unknown.has(name))continue;
  const field=fields.get(name);
  if(!field||scope!==undefined&&!field.scopes.includes(scope))invalid();
  out[name]=parseValue(field,value);
 }
 return Object.freeze(out);
}

/** Parser for the `data` of `GET`/`PATCH /v1/preferences`. A registry field this client cannot
 * read is left out with its values, and the rest of the snapshot is kept. Everything else is
 * exact: the device class, the registry revision, each document's scope, revision and digest,
 * and every value and clamped key, which has to be one the published registry knows. */
export function parsePreferenceSnapshot(raw:unknown):PreferenceSnapshot {
 if(!object(raw))invalid();
 if(!deviceClasses.includes(raw.deviceClass as DeviceClass))invalid();
 if(!object(raw.registry)||!text(raw.registry.revision,64)||raw.registry.revision!==raw.registryRevision)invalid();
 if(!Array.isArray(raw.registry.fields)||raw.registry.fields.length<1||raw.registry.fields.length>500)invalid();
 const fields=new Map<string,PreferenceField>();
 const parsed:PreferenceField[]=[];
 const unknown=new Set<string>();
 for(const entry of raw.registry.fields){
  let field:PreferenceField|undefined;try{field=parseField(entry);}catch{field=undefined;}
  // The first field published under a key is the one that counts, readable or not.
  if(!field||fields.has(field.key)||unknown.has(field.key)){
   const name=field?field.key:object(entry)&&typeof entry.key==='string'&&entry.key.length<=160?entry.key:undefined;
   if(name!==undefined&&!fields.has(name))unknown.add(name);
   noteSkipped('preferences','registry.fields');continue;
  }
  fields.set(field.key,field);parsed.push(field);
 }
 if(raw.deviceBound!==undefined&&typeof raw.deviceBound!=='boolean')invalid();
 const expectedScopes=raw.deviceBound===true?preferenceScopes:preferenceScopes.slice(0,2);
 if(!Array.isArray(raw.documents)||raw.documents.length!==expectedScopes.length)invalid();
 const seen=new Set<PreferenceScope>();
 const documents=raw.documents.map(entry=>{
  if(!object(entry)||!expectedScopes.includes(entry.scope as PreferenceScope)||seen.has(entry.scope as PreferenceScope))invalid();
  seen.add(entry.scope as PreferenceScope);
  if(!counter(entry.revision)||entry.revision<1||entry.activeRevision!==entry.revision)invalid();
  if(typeof entry.digest!=='string'||!/^[a-f0-9]{64}$/.test(entry.digest))invalid();
  return Object.freeze({scope:entry.scope as PreferenceScope,revision:entry.revision,digest:entry.digest,
   activeRevision:entry.activeRevision as number,values:parseValues(fields,entry.values,entry.scope as PreferenceScope,unknown)});
 });
 const effective=parseValues(fields,raw.effective,undefined,unknown);
 if(Object.keys(effective).length!==fields.size)invalid();
 if(!object(raw.effectiveSource))invalid();
 const source:Record<string,string>={};
 for(const [name,value] of Object.entries(raw.effectiveSource)){
  if(unknown.has(name))continue;
  const field=fields.get(name);
  if(!field||!text(value,40)||value!=='default'&&value!=='policy'&&!field.scopes.includes(value as PreferenceScope))invalid();
  source[name]=value;
 }
 if(Object.keys(source).length!==fields.size)invalid();
 if(!Array.isArray(raw.clampedFields)||raw.clampedFields.length>fields.size+unknown.size)invalid();
 const clamped=new Set<string>();
 for(const name of raw.clampedFields){if(typeof name==='string'&&unknown.has(name))continue;if(typeof name!=='string'||!fields.has(name)||clamped.has(name))invalid();clamped.add(name);}
 return Object.freeze({deviceClass:raw.deviceClass as DeviceClass,
  deviceBound:raw.deviceBound===true,registry:Object.freeze({revision:raw.registry.revision,fields:Object.freeze(parsed)}),
  documents:Object.freeze(documents),effective,effectiveSource:Object.freeze(source),
  clampedFields:Object.freeze([...clamped]),registryRevision:raw.registryRevision as string});
}

/** Compare only inside one selected viewer/device scope. Scope owners reset
 * their snapshot before accepting responses for another identity. */
export function currentPreferenceSnapshot(current:PreferenceSnapshot|undefined,incoming:PreferenceSnapshot):PreferenceSnapshot {
 if(current&&current.deviceClass===incoming.deviceClass&&incoming.documents.some(next=>{
  const old=current.documents.find(document=>document.scope===next.scope);
  return old!==undefined&&old.revision>next.revision;
 }))return current;
 return incoming;
}

/** Reads one effective value with the registry default as the only fallback. */
export function preferenceValue<T extends PreferenceValue>(snapshot:PreferenceSnapshot,name:string):T {
 const value=snapshot.effective[name];
 if(value!==undefined)return value as T;
 const field=snapshot.registry.fields.find(entry=>entry.key===name);
 if(!field)throw new Error(`This server does not publish the preference ${name}.`);
 return field.default as T;
}

/** Groups the registry in publication order for a generated settings page. */
export function preferenceGroups(registry:PreferenceRegistry):readonly Readonly<{group:string;fields:readonly PreferenceField[]}>[] {
 const order:string[]=[];const byGroup=new Map<string,PreferenceField[]>();
 for(const field of registry.fields){
  if(!byGroup.has(field.group)){byGroup.set(field.group,[]);order.push(field.group);}
  byGroup.get(field.group)!.push(field);
 }
 return Object.freeze(order.map(group=>Object.freeze({group,fields:Object.freeze(byGroup.get(group)!)})));
}

/** Rejects a patch the published registry would refuse, before it is sent. */
export function validatePreferencePatch(registry:PreferenceRegistry,scope:PreferenceScope,patch:PreferencePatch):PreferencePatch {
 const out:Record<string,PreferenceValue|null>={};
 for(const [name,value] of Object.entries(patch)){
  const field=registry.fields.find(entry=>entry.key===name);
  if(!field)throw new Error(`This server does not publish the preference ${name}.`);
  if(!field.scopes.includes(scope))throw new Error(`The preference ${name} cannot be saved in the ${scope} scope.`);
  if(value===null){out[name]=null;continue;}
  const parsed=parseValue(field,value);
  if(field.allowedValues&&typeof parsed!=='object'&&!field.allowedValues.includes(parsed as number|string))throw new Error(`${String(parsed)} is not an accepted value for ${name}.`);
  if(typeof parsed==='number'&&(field.min!==undefined&&parsed<field.min||field.max!==undefined&&parsed>field.max))throw new Error(`${String(parsed)} is outside the accepted range for ${name}.`);
  out[name]=parsed;
 }
 return Object.freeze(out);
}

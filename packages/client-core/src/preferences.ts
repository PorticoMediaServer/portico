import {unreadableServerResponse} from './server-messages.ts';
import {noteSkipped} from './tolerant.ts';
/**
 * A profile's own settings, as `GET /v1/preferences` publishes them: the list of settings with their
 * defaults (the registry), what the profile saved everywhere, for this kind of device and for this
 * device, and the value in force for each with where it came from. The server owns every default
 * and limit: an app draws its settings from the list it is handed and never invents one.
 *
 * Each setting is saved at the scopes the list names; for most that is exactly one. Quality and
 * delivery are saved for one device only, so two phones on one profile keep their own.
 */
export type PreferenceScope='profile-server'|'profile-device-class'|'profile-device';
export type DeviceClass='web'|'mobile'|'tablet'|'television';
/** `locale` is a language tag or "auto"; `languageList` is language tags, most wanted first. */
export type PreferenceType='boolean'|'integer'|'number'|'string'|'string-list'|'locale'|'languageList';
export type PreferenceValue=boolean|number|string|readonly string[];
export type PreferenceField=Readonly<{
 key:string;type:PreferenceType;default:PreferenceValue;allowedValues?:readonly (number|string)[];
 min?:number;max?:number;scopes:readonly PreferenceScope[];group:string;labelKey:string}>;
export type PreferenceRegistry=Readonly<{revision:string;fields:readonly PreferenceField[]}>;
export type PreferenceValues=Readonly<Record<string,PreferenceValue>>;
/** What the profile saved at one scope. */
export type PreferenceDocument=Readonly<{scope:PreferenceScope;values:PreferenceValues}>;
/** `effectiveSource` names, for each setting, the scope its value came from, or `default`. */
export type PreferenceSnapshot=Readonly<{
 deviceClass:DeviceClass;deviceBound:boolean;registry:PreferenceRegistry;documents:readonly PreferenceDocument[];
 effective:PreferenceValues;effectiveSource:Readonly<Record<string,string>>}>;
/** A patch carries only the keys it changes. `null` takes that scope's value away, so the next
 * wider scope's value, or the default, applies again. */
export type PreferencePatch=Readonly<Record<string,PreferenceValue|null>>;

export const preferenceScopes:readonly PreferenceScope[]=['profile-server','profile-device-class','profile-device'];
const deviceClasses:readonly DeviceClass[]=['web','mobile','tablet','television'];
const preferenceTypes:readonly PreferenceType[]=['boolean','integer','number','string','string-list','locale','languageList'];
const object=(v:unknown):v is Record<string,unknown>=>typeof v==='object'&&v!==null&&!Array.isArray(v);
const finite=(v:unknown):v is number=>typeof v==='number'&&Number.isFinite(v);
const text=(v:unknown,max:number):v is string=>typeof v==='string'&&v.length>0&&v.length<=max;
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

/** A setting this app cannot read (a type it does not know, a definition it cannot make sense of) is
 * left out, with its values; the rest of the list still draws. */
function parseField(raw:unknown):PreferenceField|undefined {
 if(!object(raw)||!text(raw.key,160)||!preferenceTypes.includes(raw.type as PreferenceType))return undefined;
 if(!text(raw.group,64)||!text(raw.labelKey,160)||!Array.isArray(raw.scopes))return undefined;
 const scopes=preferenceScopes.filter(scope=>(raw.scopes as unknown[]).includes(scope));
 if(!scopes.length)return undefined;
 let allowed:readonly (number|string)[]|undefined;
 if(raw.allowedValues!==undefined){
  if(!Array.isArray(raw.allowedValues)||!raw.allowedValues.length)return undefined;
  // An option is a bare value, or a labelled option {value, label, …}.
  const values=raw.allowedValues.map(entry=>object(entry)?entry.value:entry);
  if(!values.every(entry=>text(entry,160)||finite(entry)))return undefined;
  allowed=Object.freeze([...values as (number|string)[]]);
 }
 const field={key:raw.key,type:raw.type as PreferenceType,...(allowed?{allowedValues:allowed}:{}),
  ...(finite(raw.min)?{min:raw.min}:{}),...(finite(raw.max)?{max:raw.max}:{}),scopes:Object.freeze(scopes),group:raw.group,labelKey:raw.labelKey};
 try{return Object.freeze({...field,default:parseValue(field as PreferenceField,raw.default)});}catch{return undefined;}
}

/** The values of known settings; anything else is passed over. */
function readValues(fields:ReadonlyMap<string,PreferenceField>,raw:unknown,keep:(field:PreferenceField)=>boolean=()=>true):Record<string,PreferenceValue> {
 const out:Record<string,PreferenceValue>={};
 if(!object(raw))return out;
 for(const [name,value] of Object.entries(raw)){
  const field=fields.get(name);if(!field||!keep(field))continue;
  try{out[name]=parseValue(field,value);}catch{noteSkipped('preferences','values');}
 }
 return out;
}

/** Reads the answer of `GET` and `PATCH /v1/preferences`. Fields this app does not know are ignored;
 * a setting it cannot read is left out. Only an answer with no device class or no list is refused. */
export function parsePreferenceSnapshot(raw:unknown):PreferenceSnapshot {
 if(!object(raw)||!deviceClasses.includes(raw.deviceClass as DeviceClass)||!object(raw.registry)||!Array.isArray(raw.registry.fields))invalid();
 const fields=new Map<string,PreferenceField>();
 for(const entry of raw.registry.fields){
  const field=parseField(entry);
  // The first setting published under a key is the one that counts.
  if(!field||fields.has(field.key)){noteSkipped('preferences','registry.fields');continue;}
  fields.set(field.key,field);
 }
 const deviceBound=raw.deviceBound===true;
 const documents=preferenceScopes.filter(scope=>deviceBound||scope!=='profile-device').map(scope=>{
  const found=Array.isArray(raw.documents)?raw.documents.find(entry=>object(entry)&&entry.scope===scope):undefined;
  return Object.freeze({scope,values:Object.freeze(readValues(fields,object(found)?found.values:undefined,field=>field.scopes.includes(scope)))});
 });
 const effective=readValues(fields,raw.effective),sources=object(raw.effectiveSource)?raw.effectiveSource:{},source:Record<string,string>={};
 for(const field of fields.values()){
  // A setting the answer gave no value for is at its default.
  if(!Object.hasOwn(effective,field.key)){effective[field.key]=field.default;source[field.key]='default';continue;}
  const from=sources[field.key];
  source[field.key]=typeof from==='string'&&(from==='default'||field.scopes.includes(from as PreferenceScope))?from:'default';
 }
 return Object.freeze({deviceClass:raw.deviceClass as DeviceClass,deviceBound,
  registry:Object.freeze({revision:text(raw.registry.revision,128)?raw.registry.revision:'',fields:Object.freeze([...fields.values()])}),
  documents:Object.freeze(documents),effective:Object.freeze(effective),effectiveSource:Object.freeze(source)});
}

/**
 * The snapshot as it is once a patch saved at `scope` is applied: what the server will answer when it
 * gets the edit. Used to show an edit made while the server is out of reach, and to draw a save at once.
 */
export function withPreferencePatch(snapshot:PreferenceSnapshot,scope:PreferenceScope,patch:PreferencePatch):PreferenceSnapshot {
 const documents=snapshot.documents.map(document=>{
  if(document.scope!==scope)return document;
  const values:Record<string,PreferenceValue>={...document.values};
  for(const [key,value] of Object.entries(patch)){if(value===null)delete values[key];else values[key]=value;}
  return Object.freeze({scope,values:Object.freeze(values)});
 });
 const effective:Record<string,PreferenceValue>={...snapshot.effective},source:Record<string,string>={...snapshot.effectiveSource};
 for(const key of Object.keys(patch)){
  const field=snapshot.registry.fields.find(entry=>entry.key===key);if(!field)continue;
  // The narrowest scope that holds a value wins; with none, the default.
  const holder=[...documents].reverse().find(document=>Object.hasOwn(document.values,key));
  effective[key]=holder?holder.values[key]!:field.default;source[key]=holder?holder.scope:'default';
 }
 return Object.freeze({...snapshot,documents:Object.freeze(documents),effective:Object.freeze(effective),effectiveSource:Object.freeze(source)});
}

/** Reads one value in force, with the published default as the only fallback. */
export function preferenceValue<T extends PreferenceValue>(snapshot:PreferenceSnapshot,name:string):T {
 const value=snapshot.effective[name];
 if(value!==undefined)return value as T;
 const field=snapshot.registry.fields.find(entry=>entry.key===name);
 if(!field)throw new Error(`This server does not publish the preference ${name}.`);
 return field.default as T;
}

/** Groups the list in published order for a generated settings page. */
export function preferenceGroups(registry:PreferenceRegistry):readonly Readonly<{group:string;fields:readonly PreferenceField[]}>[] {
 const order:string[]=[];const byGroup=new Map<string,PreferenceField[]>();
 for(const field of registry.fields){
  if(!byGroup.has(field.group)){byGroup.set(field.group,[]);order.push(field.group);}
  byGroup.get(field.group)!.push(field);
 }
 return Object.freeze(order.map(group=>Object.freeze({group,fields:Object.freeze(byGroup.get(group)!)})));
}

/** Refuses, before it is sent, a patch the server would refuse. */
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

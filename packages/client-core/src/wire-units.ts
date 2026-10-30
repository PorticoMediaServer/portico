import {unreadableServerResponse} from './server-messages.ts';
/** API instants are RFC 3339 and media times are integer milliseconds.
 * Parsers expose the units existing UI models use; only this boundary converts. */
export function wireInstant(value:unknown):number|null {
 if(value===null)return null;
 if(value==='')return 0;
 if(typeof value!=='string'||!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z$/.test(value))throw new Error(unreadableServerResponse);
 const at=Date.parse(value);
 if(!Number.isSafeInteger(at)||at<0)throw new Error(unreadableServerResponse);
 return at;
}
export function wireInstants(value:unknown,keys:readonly string[]):unknown {
 if(typeof value!=='object'||value===null||Array.isArray(value))return value;
 const out={...(value as Record<string,unknown>)};
 for(const key of keys)if(key in out)out[key]=wireInstant(out[key]);
 return out;
}
export function wireDurations(value:unknown,fields:Readonly<Record<string,string>>):unknown {
 if(typeof value!=='object'||value===null||Array.isArray(value))return value;
 const out={...(value as Record<string,unknown>)};
 for(const [internal,wire] of Object.entries(fields)){
  if(internal in out)throw new Error(unreadableServerResponse);
  if(!(wire in out))continue;
  const ms=out[wire];delete out[wire];
  if(ms===null){out[internal]=null;continue;}
  if(typeof ms!=='number'||!Number.isSafeInteger(ms)||ms<0)throw new Error(unreadableServerResponse);
  out[internal]=ms/1000;
 }
 return out;
}
export function durationIntent<T extends Record<string,unknown>>(value:T,fields:Readonly<Record<string,string>>):Record<string,unknown>{
 const out:Record<string,unknown>={...value};
 for(const [internal,wire] of Object.entries(fields)){
  if(!(internal in out))continue;
  const seconds=out[internal];delete out[internal];
  if(seconds===null){out[wire]=null;continue;}
  if(typeof seconds!=='number'||!Number.isFinite(seconds)||seconds<0)throw new Error(unreadableServerResponse);
  out[wire]=Math.round(seconds*1000);
 }
 return out;
}
/** Display-only media fields degrade individually; identity and write receipts stay exact. */
export function wireDisplayDurations(value:unknown,fields:Readonly<Record<string,string>>,left:(part:string,value:unknown)=>void):unknown {
 if(typeof value!=='object'||value===null||Array.isArray(value))return value;
 const out={...(value as Record<string,unknown>)};
 for(const [internal,wire] of Object.entries(fields)){
  delete out[internal];
  if(!(wire in out))continue;
  const ms=out[wire];delete out[wire];
  if(ms===undefined||ms===null)continue;
  if(typeof ms==='number'&&Number.isSafeInteger(ms)&&ms>=0)out[internal]=ms/1000;
  else left(wire,ms);
 }
 return out;
}

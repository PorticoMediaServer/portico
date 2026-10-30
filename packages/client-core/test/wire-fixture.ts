/** Older tests express their expectations in UI units. Adapt only mock server answers to the wire contract. */
export function wireFixture<T>(value:T,fields:Readonly<Record<string,string>>={},instants:readonly string[]=[]):T {
 if(Array.isArray(value))return value.map(v=>wireFixture(v,fields,instants)) as T;
 if(!value||typeof value!=='object')return value;
 const out:Record<string,unknown>={};
 for(const [key,v] of Object.entries(value)){
  if(fields[key])out[fields[key]]=typeof v==='number'?v*1000:v;
  else if(instants.includes(key)&&typeof v==='number'&&Number.isFinite(v)&&v>=0)out[key]=v===0?'':new Date(v).toISOString();
  else out[key]=wireFixture(v,fields,instants);
 }
 return out as T;
}

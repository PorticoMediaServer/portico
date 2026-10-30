import {readRecord,writeRecord,serialized} from './storage';
import {base64,fromBase64,utf8,fromUtf8,concat} from './encoding';
import {emptyUserMutationState,validateUserMutationState,sameUserMutationScope, type UserMutationScope} from '../../../../packages/client-core/src/user-mutations';
const prefix='user-mutations-v1:',pageBytes=180000;
/** Vega's sole SDK storage owner serializes the full comparison and commit.
 * Existing two-slot records make the manifest the crash-safe commit point. */
export function userMutationTransaction(operation:string,json:string):Promise<string|boolean>{
 return serialized(prefix,async()=>{
  const payload=JSON.parse(json),raw=await readRecord('queues',prefix+'manifest',{strict:true});
  const manifest=raw===null?null:JSON.parse(raw);
  if(raw!==null&&(!manifest||typeof manifest!=='object'||Array.isArray(manifest)))throw new Error('mutation_store_damaged');
  let state=emptyUserMutationState();
  if(manifest){
   if(![0,1].includes(manifest.bank)||!Number.isInteger(manifest.pages)||manifest.pages<1||manifest.pages>47)throw new Error('mutation_store_damaged');
   const parts:Uint8Array[]=[];
   for(let i=0;i<manifest.pages;i++){
    const rawPage=await readRecord('queues',`${prefix}${manifest.bank}:${i}`);
    const part=rawPage===null?null:fromBase64(rawPage);
    if(!part||part.length>pageBytes)throw new Error('mutation_store_damaged');parts.push(part);
   }
   const bytes=concat(...parts);if(bytes.length>8388608)throw new Error('mutation_store_damaged');
   const text=fromUtf8(bytes);if(text===null)throw new Error('mutation_store_damaged');state=validateUserMutationState(JSON.parse(text));
   if(state.revision!==manifest.revision)throw new Error('mutation_store_damaged');
  }
  if(operation==='read')return JSON.stringify(state);
  let next;
  if(operation==='cas'){
   if(state.revision!==payload.expectedRevision)return false;
   next=validateUserMutationState(payload.next);
  }else if(operation==='scope'||operation==='retireScope'){
   if(operation==='retireScope'){const active=await readRecord('queues',prefix+'active',{strict:true});if(active===null||!JSON.parse(active)||!sameUserMutationScope(JSON.parse(active),payload.scope))return false;}
   const scope=operation==='retireScope'?null:payload.scope;
   next=validateUserMutationState(JSON.parse(JSON.stringify(state)));
   for(const entry of next.entries)if(entry.state==='pending'&&(!scope||!sameUserMutationScope(entry.scope,scope as UserMutationScope))){entry.state='quarantined';entry.errorCode='mutation_scope_changed';delete entry.leaseOwner;delete entry.leaseUntilMs;}
   next.revision++;
   await writeRecord('queues',prefix+'active',JSON.stringify(scope??null));
  }else throw new Error('mutation_store_operation');
  if(next.revision!==state.revision+1)throw new Error('mutation_store_damaged');
  const bytes=utf8(JSON.stringify(validateUserMutationState(next))),bank=1-(manifest?.bank??0),pages=Math.ceil(bytes.length/pageBytes);
  for(let i=0;i<pages;i++)await writeRecord('queues',`${prefix}${bank}:${i}`,base64(bytes.subarray(i*pageBytes,(i+1)*pageBytes)));
  await writeRecord('queues',prefix+'manifest',JSON.stringify({bank,pages,revision:next.revision}));
  return true;
 });
}

/** Root50: the key describes the original accepted operation, never the viewer
 * present when storage happens to load. Legacy token-only values are ignored. */
export type CastControllerScope = Readonly<{serverId:string;authority:string;accountId:string;profileId:string;senderDeviceId:string;senderFamilyId:string}>;
export type CastControllerContext = CastControllerScope & Readonly<{publicKey:string;origin:string}>;
export type CastControllerBinding = Readonly<{id:string;bootstrapId:string;receiverId:string;targetId:string;targetFamilyId:string;targetGeneration:string;scope:CastControllerScope}>;
export type CastControllerProof = Readonly<{version:2;context:CastControllerContext;binding:CastControllerBinding;controlToken:string}>;
const keys=['serverId','authority','accountId','profileId','senderDeviceId','senderFamilyId'] as const;
const bounded=(v:unknown,max=256):v is string=>typeof v==='string'&&v.length>0&&v.length<=max&&!/[\u0000-\u001f]/.test(v);
export function castScopeEqual(a:CastControllerScope,b:CastControllerScope){return keys.every(k=>a[k]===b[k]);}
export function castBinding(raw:unknown):CastControllerBinding|undefined {
 if(!raw||typeof raw!=='object'||Array.isArray(raw))return;
 const r=raw as Record<string,unknown>,s=r.scope as Record<string,unknown>|undefined;
 if(!s||!keys.every(k=>bounded(s[k]))||!['id','bootstrapId','receiverId','targetId','targetFamilyId','targetGeneration'].every(k=>bounded(r[k]))||!/^\d+$/.test(String(r.targetGeneration)))return;
 return Object.freeze({id:r.id as string,bootstrapId:r.bootstrapId as string,receiverId:r.receiverId as string,targetId:r.targetId as string,targetFamilyId:r.targetFamilyId as string,targetGeneration:r.targetGeneration as string,scope:Object.freeze(Object.fromEntries(keys.map(k=>[k,s[k]]))) as CastControllerScope});
}
export function castContext(raw:CastControllerContext|undefined):CastControllerContext|undefined {
 if(!raw||!keys.every(k=>bounded(raw[k]))||!/^[-_A-Za-z0-9]{43}$/.test(raw.publicKey))return;
 try{if(new URL(raw.origin).origin!==raw.origin||!raw.origin.startsWith('https://'))return;}catch{return;}
 return Object.freeze({...raw});
}
export function castProofKey(context:CastControllerContext,receiverId:string){return 'portico.cast.controller:v2:'+JSON.stringify([...keys.map(k=>context[k]),context.publicKey,context.origin,receiverId]);}
export function readCastProof(value:string|undefined,context:CastControllerContext,receiverId:string):CastControllerProof|undefined {
 if(!value||value.length>4096)return;
 try{const r=JSON.parse(value),binding=castBinding(r.binding),original=castContext(r.context);
 if(r.version!==2||!binding||!original||!castScopeEqual(original,context)||original.publicKey!==context.publicKey||original.origin!==context.origin||!castScopeEqual(binding.scope,original)||binding.receiverId!==receiverId||typeof r.controlToken!=='string'||!/^[a-f0-9]{48}$/.test(r.controlToken))return;
 return Object.freeze({version:2,context:original,binding,controlToken:r.controlToken});
 }catch{return;}
}

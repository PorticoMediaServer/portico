/** Facts from the validated private receiver channel, never a local player fence
 * or a media/queue grant. The binding names an immutable server-owned tuple. */
export type CastControllerStamp=Readonly<{bindingId:string;controllerGeneration:number;connectionGeneration:number}>;
export type CastReceiverOccurrence=Readonly<{sessionId:string;generation:number;itemId:string}>;
export type CastRemoteIdentity=Readonly<{controller:CastControllerStamp;occurrence:CastReceiverOccurrence}>;
const object=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==='object'&&!Array.isArray(v);
const id=(v:unknown):v is string=>typeof v==='string'&&/^[A-Za-z0-9._:-]{1,128}$/.test(v);
const generation=(v:unknown):v is number=>typeof v==='number'&&Number.isSafeInteger(v)&&v>0;
export function castControllerStamp(raw:unknown):CastControllerStamp|undefined{
 if(!object(raw)||!id(raw.bindingId)||!generation(raw.controllerGeneration)||!generation(raw.connectionGeneration))return;
 return Object.freeze({bindingId:raw.bindingId,controllerGeneration:raw.controllerGeneration,connectionGeneration:raw.connectionGeneration});
}
export function castReceiverOccurrence(raw:unknown):CastReceiverOccurrence|undefined{
 if(!object(raw)||!id(raw.sessionId)||!id(raw.itemId)||!generation(raw.generation))return;
 return Object.freeze({sessionId:raw.sessionId,itemId:raw.itemId,generation:raw.generation});
}
export function castRemoteIdentity(raw:unknown):CastRemoteIdentity|undefined{
 if(!object(raw))return;const controller=castControllerStamp(raw.controller),occurrence=castReceiverOccurrence(raw.occurrence);
 return controller&&occurrence?Object.freeze({controller,occurrence}):undefined;
}
export function sameCastController(a:CastControllerStamp|undefined,b:CastControllerStamp|undefined):boolean{return !!a&&!!b&&a.bindingId===b.bindingId&&a.controllerGeneration===b.controllerGeneration&&a.connectionGeneration===b.connectionGeneration;}
export function sameCastRemote(a:CastRemoteIdentity|undefined,b:CastRemoteIdentity|undefined):boolean{return !!a&&!!b&&sameCastController(a.controller,b.controller)&&a.occurrence.sessionId===b.occurrence.sessionId&&a.occurrence.generation===b.occurrence.generation&&a.occurrence.itemId===b.occurrence.itemId;}

import {parseLocalSession} from './local-session.ts';
import type {LocalSession,Viewer} from './index.ts';

/** This record is an expiring server credential, never an account credential. */
export type SelectedViewerRecord=Readonly<{version:1;serverUrl:string;session:LocalSession}>;
export interface SelectedViewerStorage {
 read():Promise<SelectedViewerRecord|null>;write(record:SelectedViewerRecord|null):Promise<void>;
 /** Removes the saved record only when it is the given sign-in's, as one step where the store can. Absent: read, compare, then write. */
 clearIfFamily?(sessionFamilyId:string):Promise<void>;
}
export type SelectionHandoff={
 current():SelectedViewerRecord|null;
 /** Fence playback, asynchronous reads, subscriptions and private UI before returning. */
 fence():void;
 verify(record:SelectedViewerRecord,signal:AbortSignal):Promise<{viewer:Viewer}>;
 assertCurrent():void;
 /** Candidate cancellation does not revoke the authority to restore the prior viewer. */
 assertCandidateCurrent?():void;
 prepareContext?():Promise<void>;
 publish(record:SelectedViewerRecord):void;
 restore?(record:SelectedViewerRecord):void;
 revoke(record:SelectedViewerRecord):Promise<void>;
 cleanupFailed?(record:SelectedViewerRecord):void;
};
export function selectedViewerRecord(serverUrl:string,raw:unknown,stored=false):SelectedViewerRecord {
 const url=new URL(serverUrl);if(!['https:','http:'].includes(url.protocol)||url.username||url.password||url.search||url.hash)throw new Error('A server origin without credentials is required.');
 // The refresh credential stays in the protected connection store only (see session-refresh.ts).
 const {refreshToken:_refresh,...session}=parseLocalSession(raw,{},{stored});
 return Object.freeze({version:1,serverUrl:url.href.replace(/\/+$/,''),session:Object.freeze(session)});
}
export function parseSelectedViewerRecord(raw:unknown):SelectedViewerRecord|null {
 if(raw===null)return null;
 if(!raw||typeof raw!=='object'||Array.isArray(raw)||!('version'in raw)||raw.version!==1||!('serverUrl'in raw)||typeof raw.serverUrl!=='string'||!('session'in raw))throw new Error('The saved server selection is invalid.');
 // A saved selection may have expired since; the server decides that when it is verified.
 return selectedViewerRecord(raw.serverUrl,raw.session,true);
}
/** The same sign-in: one server and one session family. A refreshed access token is still the same
 * sign-in, so nothing here ever retires a family because its bearer string changed. */
export function sameSignIn(a:SelectedViewerRecord|null|undefined,b:SelectedViewerRecord|null|undefined):boolean {
 return !!a&&!!b&&a.serverUrl===b.serverUrl&&a.session.sessionFamilyId===b.session.sessionFamilyId;
}
/** One device handoff at a time. An unused candidate is always explicitly retired.
 * Account proof and server/profile proof must be checked by the caller's scope guard.
 * A storage/verification failure never publishes a candidate or logs out the account.
 */
export class ViewerSelectionCommit {
 private serial:Promise<void>=Promise.resolve();private active=false;private clearing?:Promise<void>;private generation=0;private abort?:AbortController;private candidate?:SelectedViewerRecord;
 readonly storage:SelectedViewerStorage;
 constructor(storage:SelectedViewerStorage){this.storage=storage;}
 async commit(candidate:SelectedViewerRecord,h:SelectionHandoff):Promise<void>{
  if(this.active||this.clearing){
   // A duplicated UI submission can carry the same established credential. It
   // is not an unused family and must not retire the handoff already using it.
   let current:SelectedViewerRecord|null=null;try{current=h.current();}catch{}
   if(!sameSignIn(this.candidate,candidate)&&!sameSignIn(current,candidate))await h.revoke(candidate).catch(()=>h.cleanupFailed?.(candidate));
   throw new Error('Another profile selection is being saved.');
  }
  this.active=true;this.candidate=candidate;const generation=++this.generation,abort=new AbortController();this.abort=abort;
  let finish!:()=>void;this.serial=new Promise<void>(resolve=>{finish=resolve;});
  let previous:SelectedViewerRecord|null=null,old:SelectedViewerRecord|null=null,stored=false,published=false,fenced=false;
  const assert=()=>{if(generation!==this.generation||abort.signal.aborted)throw new Error('Profile selection was cancelled.');h.assertCurrent();h.assertCandidateCurrent?.();};
  const timer=setTimeout(()=>abort.abort(),20000);
  try{
   assert();old=h.current();previous=await this.storage.read();assert();fenced=true;h.fence();assert();
   stored=true;await this.storage.write(candidate);assert();
   const me=await abortableVerification(h.verify(candidate,abort.signal),abort.signal);assert();
   if(!me?.viewer||(['authority','accountId','profileId','serverId','role']as const).some(k=>me.viewer[k]!==candidate.session.viewer[k]))throw new Error('The verified server viewer does not match this profile selection.');
   await h.prepareContext?.();assert();h.publish(candidate);published=true;
  }catch(error){
   if(generation===this.generation&&fenced){
    try{
     if(stored)await this.storage.write(previous);
     // Sign-out may arrive while the rollback write is in flight. Its clear
     // follows this write, but its already-fenced UI must never be resurrected.
     if(generation===this.generation){
      h.assertCurrent();
      // Rebuild the previous viewer without restarting its player.
      if(old&&sameSignIn(previous,old))h.restore?.(old);
     }
    }catch{
     // A failed rollback cannot leave either credential eligible for recovery.
     await this.storage.write(null).catch(()=>{});
     if(old){const prior=old;void h.revoke(prior).catch(()=>h.cleanupFailed?.(prior));}
     throw new Error('The profile change could not be rolled back safely. This device is signed out of the server; account credentials are unchanged.');
    }
   }
   throw error;
  }finally{
   clearTimeout(timer);this.abort=undefined;this.candidate=undefined;this.active=false;finish();
   // Retirement is by sign-in (server and family): an unused candidate of the family already in
   // use, or a previous viewer that is the candidate's own family refreshed, is never revoked.
   if(!published&&!sameSignIn(candidate,old))void h.revoke(candidate).catch(()=>h.cleanupFailed?.(candidate));
   if(published&&old&&!sameSignIn(old,candidate)){const prior=old;void h.revoke(prior).catch(()=>h.cleanupFailed?.(prior));}
  }
 }
 /** Linearizes sign-out after any in-flight storage write.
  * `retiring` is the sign-in that is ending here. The saved record is shared by every tab of the
  * browser: when another tab has since chosen a different profile, the record is that tab's, and
  * an old tab finding its own sign-in ended must not erase it. Without `retiring` (nothing was
  * signed in here, or the person asked to forget the server) the record is removed whatever it is.
  */
 async clear(retiring?:Readonly<{sessionFamilyId:string}>):Promise<void>{
  if(this.clearing)return this.clearing;
  ++this.generation;this.abort?.abort();const pending=this.serial;
  const task=(async()=>{
   await pending;
   if(!retiring){await this.storage.write(null);return;}
   if(this.storage.clearIfFamily){await this.storage.clearIfFamily(retiring.sessionFamilyId);return;}
   let saved:SelectedViewerRecord|null=null,readable=true;
   try{saved=await this.storage.read();}catch{readable=false;}
   // A record that cannot be read is no use to any tab: it goes, as before.
   if(!readable||saved?.session.sessionFamilyId===retiring.sessionFamilyId)await this.storage.write(null);
  })();this.clearing=task;
  try{await task;}finally{if(this.clearing===task)this.clearing=undefined;}
 }
}

/** A transport may ignore AbortSignal. Cancellation must still release sign-out;
 * its late result has no authority to publish or restore a viewer. Storage writes
 * are deliberately NOT raced, because clear must follow their durable completion.
 */
async function abortableVerification<T>(pending:Promise<T>,signal:AbortSignal):Promise<T>{
 let cancelled!:()=>void;
 try{return await Promise.race([pending,new Promise<never>((_,reject)=>{
  cancelled=()=>reject(new Error('Profile selection was cancelled.'));
  signal.addEventListener('abort',cancelled,{once:true});if(signal.aborted)cancelled();
 })]);}finally{signal.removeEventListener('abort',cancelled);}
}

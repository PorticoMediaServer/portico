import {parseAppSessionRecord} from '../../../../packages/client-core/src/app-sessions';
import {subscribeNativeAppSessions} from './app-session-storage';
import {NativeQueueStore} from './queue-storage';
import {NativeUserMutationSelectionOwner} from './user-mutation-selection';
import {NativeModules} from 'react-native';
import {validateUserMutationState, type DurableUserMutationStore, type UserMutationScope, type UserMutationState, UserMutationError} from '../../../../packages/client-core/src/user-mutations';

export interface NativeUserMutationBridge {
 userMutationTransaction(operation:'read'|'cas'|'scope'|'retireScope',payload:string):Promise<string|boolean>;
}
/** Entire aggregate CAS executes in the platform storage owner. Bounded inactive
 * pages plus one durable manifest commit include every scope in the same quota. */
export class NativeUserMutationStore implements DurableUserMutationStore {
 constructor(private readonly bridge:NativeUserMutationBridge=NativeModules.PorticoRuntime){}
 private async call(operation:'read'|'cas'|'scope'|'retireScope',payload:unknown):Promise<string|boolean>{
  if(typeof this.bridge?.userMutationTransaction!=='function')throw new UserMutationError('mutation_store_unavailable');
  try{return await this.bridge.userMutationTransaction(operation,JSON.stringify(payload));}
  catch(error){if(error instanceof UserMutationError)throw error;throw new UserMutationError('mutation_store_unavailable');}
 }
 async read():Promise<UserMutationState>{
  const raw=await this.call('read',{});
  if(typeof raw!=='string')throw new UserMutationError('mutation_store_damaged');
  try{return validateUserMutationState(JSON.parse(raw));}
  catch(error){if(error instanceof UserMutationError)throw error;throw new UserMutationError('mutation_store_damaged');}
 }
 async compareAndSwap(expectedRevision:number,next:UserMutationState):Promise<boolean>{
  validateUserMutationState(next);
  if(!Number.isSafeInteger(expectedRevision)||expectedRevision<0||next.revision!==expectedRevision+1)throw new UserMutationError('mutation_store_damaged');
  const result=await this.call('cas',{expectedRevision,next});
  if(typeof result!=='boolean')throw new UserMutationError('mutation_store_damaged');
  return result;
 }
 /** Activating a different family quarantines pending work atomically. No later
  * login restores it to pending, including a login to the same account. */
 async activate(scope:UserMutationScope):Promise<void>{await this.call('scope',{scope});}
 async retire(scope:UserMutationScope):Promise<boolean>{return await this.call('retireScope',{scope})===true;}
}

// One observer per JS app lifetime, deliberately independent of shell mounts.
let selectionOwner:NativeUserMutationSelectionOwner|undefined;
export function retainNativeUserMutationSelection(store:NativeUserMutationStore,scope:UserMutationScope,report:(error:unknown)=>void):Promise<void>{
 selectionOwner??=new NativeUserMutationSelectionOwner(work=>new NativeQueueStore().withRecordLease('accounts.sessions.v1',async read=>{
  const record=parseAppSessionRecord(await read()),selected=record.servers.find(value=>value.connection.pin.serverId===record.selectedServerId),session=selected?.connection.session;
  return work(selected&&session?{serverId:session.viewer.serverId,origin:selected.connection.logicalOrigin,authority:session.viewer.authority,accountId:session.viewer.accountId,profileId:session.viewer.profileId,familyId:session.sessionFamilyId,...(session.deviceId?{deviceId:session.deviceId}:{})}:undefined);
 }),subscribeNativeAppSessions);
 return selectionOwner.activate(store,scope,report);
}

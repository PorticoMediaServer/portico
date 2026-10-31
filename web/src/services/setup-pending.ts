import type {ServicesAccount} from './session';
import {scopedHostedApproval,type HostedOwnerPending,type HostedOwnerPendingStore} from './owner-pending';
import {reviewHostedSetupClaim,decideHostedSetupClaim,completeHostedSetupClaim,type HostedSetupClaimReview} from './setup-claim';
/** The intake holds an opaque approval code only. The separate setup validators
 * require actual signed setup custody at Hosted, never owner-switch authority. */
export function scopedHostedSetupClaim(account:ServicesAccount,store:HostedOwnerPendingStore,pending:HostedOwnerPending){
 let observed:HostedSetupClaimReview|undefined;
 const approval=scopedHostedApproval(account,store,pending,{review:reviewHostedSetupClaim,decide:decideHostedSetupClaim,
  complete:(a,review,signal)=>completeHostedSetupClaim(a,review,signal,current=>{observed=current;})});
 return {...approval,
  /** Return when installed, else the one lookup that said how far along it is. */
  async complete():Promise<{location?:string;review?:HostedSetupClaimReview}>{
   observed=undefined;
   const location=await approval.complete();
   return location?{location}:{review:observed};
  }};
}

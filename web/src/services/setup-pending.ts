import type {ServicesAccount} from './session';
import {scopedHostedApproval,type HostedOwnerPending,type HostedOwnerPendingStore} from './owner-pending';
import {reviewHostedSetupClaim,decideHostedSetupClaim,completeHostedSetupClaim} from './setup-claim';
/** The intake holds an opaque approval code only. The separate setup validators
 * require actual signed setup custody at Hosted, never owner-switch authority. */
export function scopedHostedSetupClaim(account:ServicesAccount,store:HostedOwnerPendingStore,pending:HostedOwnerPending){
 return scopedHostedApproval(account,store,pending,{review:reviewHostedSetupClaim,decide:decideHostedSetupClaim,complete:completeHostedSetupClaim});
}

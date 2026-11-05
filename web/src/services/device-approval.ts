import {createHostedAccountApi} from '@core/query/hosted-account-transport.ts';
import {SetupCodeApprover, type SetupPreview} from '@core/setup-code.ts';
import {accountIdentity, currentAccountWork, type AccountIdentity, type ServicesAccount} from './session';

export type DeviceReview = Readonly<{preview: SetupPreview; identity: AccountIdentity}>;
/** Hosted code approval has one explicit authority. It never searches local servers. */
export async function reviewHostedDevice(account: ServicesAccount, code: string, signal: AbortSignal): Promise<DeviceReview> {
  return currentAccountWork(account, async session => {
    const approver = new SetupCodeApprover(createHostedAccountApi(account.service, session, fetch), 'hosted');
    const preview = await approver.review(code, signal);
    return {preview, identity: accountIdentity(session)};
  });
}

export async function decideHostedDevice(account: ServicesAccount, review: DeviceReview, decision: 'approve' | 'deny', signal: AbortSignal): Promise<void> {
  await currentAccountWork(account, session => {
    const approver = new SetupCodeApprover(createHostedAccountApi(account.service, session, fetch), 'hosted');
    return approver.decide(review.preview, decision, signal);
  }, review.identity);
}

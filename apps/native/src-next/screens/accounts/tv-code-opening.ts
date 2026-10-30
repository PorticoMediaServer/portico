import type {TVApprovalCredentialOpening} from './tv-approval-credentials';
import type {TVApprovalFinalization, TVApprovalSnapshot} from './tv-approval-finalization';
import type {SetupCodeService, SetupCodeSnapshot} from '../../../../../packages/client-core/src/setup-code';

export type TVCodeKind = 'local' | 'portico';
export type TVCodeCapture<T, S> = Readonly<{kind: TVCodeKind; service: SetupCodeService<T>; source: S}>;
export type TVCodeOpening = Readonly<{
  kind: TVCodeKind;
  current: () => boolean;
  pending: () => boolean;
  start: () => Promise<void>;
  newCode: () => Promise<void>;
  approval: () => TVApprovalSnapshot | undefined;
  retryApproval: () => Promise<void>;
  canTypeCredentials: () => boolean;
  credentials: () => Promise<void>;
  retire: (cancel?: boolean) => void;
}>;

/** One rendered code opening owns its service, subscription and accepting source.
 * A retained action never discovers a replacement through the component's refs. */
export function createTVCodeOpening<T, S>(capture: TVCodeCapture<T, S>, options: {
  owns: (opening: TVCodeOpening) => boolean;
  current: (original: TVCodeCapture<T, S>) => boolean;
  publish: (opening: TVCodeOpening, value: Omit<SetupCodeSnapshot<T>, 'grant'>) => void;
  busy: (pending: boolean, adopting: boolean) => void;
  error: (failure: unknown) => void;
  accept: (original: TVCodeCapture<T, S>, grant: T, current: () => boolean) => Promise<void>;
  watchApproval?: (current: () => boolean, retired: () => void) => TVApprovalCredentialOpening;
  approval?: (original: TVCodeCapture<T, S>, grant: T, current: () => boolean) => TVApprovalFinalization;
  approved?: () => Promise<void>;
  approvalRetired?: () => void;
  replace: (original: TVCodeCapture<T, S>, current: () => boolean) => Promise<void>;
  credentials: (original: TVCodeCapture<T, S>, current: () => boolean) => Promise<void>;
}): TVCodeOpening {
  const original = Object.freeze({...capture});
  let phase: 'live' | 'new-code' | 'credentials' | 'adopting' | 'retired' = 'live';
  let approval: TVApprovalFinalization | undefined, approvedGrant: T | undefined, credentialOpening: TVApprovalCredentialOpening | undefined;
  let starting = false, off: (() => void) | undefined, cancellation: Promise<void> | undefined;
  const owns = () => phase !== 'retired' && options.owns(opening);
  const sourceCurrent = () => owns() && options.current(original) && owns();
  const current = () => sourceCurrent() && (!credentialOpening || credentialOpening.current()) && sourceCurrent();
  const pending = () => starting || phase !== 'live' && phase !== 'retired';
  const changed = () => {if (current()) options.busy(pending(), phase === 'adopting');};
  const unlink = () => {const release = off; off = undefined; release?.();};
  const cancel = () => {
    if (!cancellation) {
      try {cancellation = Promise.resolve(original.service.cancel());}
      catch (failure) {cancellation = Promise.reject(failure);}
    }
    return cancellation;
  };
  const transition = async (next: 'new-code' | 'credentials') => {
    if (!current() || pending() || approval && !approval.canCancel()) return;
    phase = next; unlink(); changed();
    const same = () => phase === next && current();
    try {
      if (!same()) return;
      // Unlink first: cancel emits synchronously and may settle after Back/reset.
      await cancel().catch(failure => {if (same()) cancellation = undefined; throw failure;});
      if (!same()) return;
      await (next === 'new-code' ? options.replace : options.credentials)(original, same);
    } catch (failure) {
      if (same()) {phase = 'live'; options.error(failure);}
    } finally {
      if (same()) phase = 'live';
      changed();
    }
  };
  const accept = async (grant: T) => {
    if (phase !== 'live' || !current()) return;
    phase = 'adopting'; unlink(); changed();
    const same = () => phase === 'adopting' && current();
    try {
      if (!same() || !original.service.isCurrent(grant) || !same()) return;
      if (options.approval) {
        if (!approval) approval = options.approval(original, grant, same);
        if (!same()) return;
        const result = await approval.run();
        if (result === 'retired') {if (sourceCurrent()) options.approvalRetired?.(); return;}
        if (!same()) return;
        if (result === 'complete') await options.approved?.();
        else {const value = approval.getSnapshot(); if (same() && value.error) options.error(value.error);}
      } else await options.accept(original, grant, same);
    } catch (failure) {if (same()) options.error(failure);}
    finally {if (same()) phase = 'live'; changed();}
  };
  const read = () => {
    // Refuse before even reading a retired source, not just before setState.
    if (phase !== 'live' || !current()) return;
    const value = original.service.getSnapshot();
    if (phase !== 'live' || !current()) return;
    const {grant, ...presentation} = value;
    options.publish(opening, presentation);
    if (phase !== 'live' || !current() || value.phase !== 'approved' || grant === undefined) return;
    approvedGrant = grant;
    void accept(grant);
  };
  const opening: TVCodeOpening = Object.freeze({
    kind: original.kind, current, pending,
    approval: () => current() ? approval?.getSnapshot() : undefined,
    canTypeCredentials: () => current() && (!approval || approval.canCancel()),
    async retryApproval() {
      if (!current() || pending() || !approval || approvedGrant === undefined) return;
      if (!current() || approval.getSnapshot().phase !== 'failed') return;
      await accept(approvedGrant);
    },
    async start() {
      if (!current() || starting || off) return;
      starting = true;
      try {
        if (!credentialOpening && options.watchApproval) credentialOpening = options.watchApproval(sourceCurrent, () => {if (sourceCurrent()) options.approvalRetired?.();});
        if (!current()) return;
        off = original.service.subscribe(read); changed();
        if (current()) await original.service.start();
      }
      catch (failure) {if (current()) options.error(failure);}
      finally {starting = false; changed();}
    },
    newCode: () => transition('new-code'), credentials: () => transition('credentials'),
    retire(abandon = false) {
      if (phase === 'retired') return;
      const owned = options.owns(opening), mayCancel = !approval || approval.canCancel();
      phase = 'retired'; unlink(); credentialOpening?.retire(); approval?.retire();
      if (!owned) return;
      original.service.pause();
      // Unmount only pauses. Explicit abandonment cancels this original once;
      // its late storage/network failure has no presentation owner to update.
      if (abandon && mayCancel) void cancel().catch(() => {});
    },
  });
  return opening;
}

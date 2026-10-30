import type {UserMutationRecoveryModel} from '../../../../packages/client-core/src/user-mutation-recovery';
import type {UserMutationScope} from '../../../../packages/client-core/src/user-mutations';

const scopeKey = (scope: UserMutationScope | undefined) => scope && JSON.stringify([
  scope.serverId, scope.origin, scope.authority, scope.accountId, scope.profileId, scope.familyId, scope.deviceId,
]);

/** One shell's opening. Closing only retires the presentation and its in-flight
 * reads; the shared model and durable coordinator keep custody of every change. */
export class NativePendingChanges {
  private readonly original: string | undefined;
  private readonly listeners = new Set<() => void>();
  private opening: AbortController | undefined;
  private retired = false;
  private revision = 0;
  private readonly off: () => void;

  constructor(readonly model: UserMutationRecoveryModel, private readonly scope: () => UserMutationScope | undefined,
    subscribeScope: (listener: () => void) => () => void) {
    this.original = scopeKey(scope());
    this.off = subscribeScope(() => {if (!this.current()) this.dispose();});
  }
  current = () => !this.retired && !!this.original && scopeKey(this.scope()) === this.original;
  subscribe = (listener: () => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  getSnapshot = () => this.current() && this.opening ? this.revision : 0;
  signal = () => this.opening?.signal;
  open = () => {
    if (!this.current() || this.opening) return;
    this.opening = new AbortController(); this.revision++;
    this.publish();
  };
  close = () => {
    this.opening?.abort(); this.opening = undefined;
    this.publish();
  };
  private publish() {for (const listener of [...this.listeners]) listener();}
  dispose = () => {
    if (this.retired) return;
    this.retired = true; this.close(); this.off(); this.model.dispose(); this.listeners.clear();
  };
}

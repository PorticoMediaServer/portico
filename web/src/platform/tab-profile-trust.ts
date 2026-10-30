import {parseTrustedProfile, trustScopeKey, type ProfileScope, type TrustedProfile} from '@core/profile-management.ts';

type Scope = ProfileScope & {serverId: string; profileId: string; installationId: string};
/** The drawn promise is this browser tab. PIN proofs never enter localStorage,
 * a shared IndexedDB record, another tab or the server selection model. */
export class TabProfileTrust {
  private readonly proofs = new Map<string, TrustedProfile>();
  read(scope: Scope, now = Date.now()): TrustedProfile | undefined {
    const key = trustScopeKey(scope), proof = this.proofs.get(key);
    if (!proof) return;
    try {return parseTrustedProfile(proof, scope, now);} catch {this.proofs.delete(key); return;}
  }
  save(proof: TrustedProfile) {
    const checked = parseTrustedProfile(proof, proof), key = trustScopeKey(checked);
    this.proofs.delete(key); this.proofs.set(key, checked);
    while (this.proofs.size > 64) this.proofs.delete(this.proofs.keys().next().value!);
  }
  forget(scope: Scope) {this.proofs.delete(trustScopeKey(scope));}
  forgetAccount(serverId: string, accountId: string) {
    for (const [key, proof] of this.proofs) if (proof.serverId === serverId && proof.accountId === accountId) this.proofs.delete(key);
  }
}
export const browserTabProfileTrust = new TabProfileTrust();

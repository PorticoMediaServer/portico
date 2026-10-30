import {NativeModules} from 'react-native';
import {installationIdentity, setInstallationIdentity, validInstallationId, type InstallationIdentity} from '../../../../packages/client-core/src/installation';
import {readNativeSession, nativeSelectedConnectionEnvironment} from './session';
import {profileInstallation} from './profile-trust';

const invalid = () => Object.assign(new Error('invalid_installation_metadata'), {code: 'invalid_installation_metadata'});
const cancelled = () => Object.assign(new Error('native_installation_cancelled'), {name: 'AbortError'});
const mismatch = () => Object.assign(new Error('installation_identity_mismatch'), {code: 'identity_mismatch'});

/** A host owns restore generations; platform adapters supply only validated device metadata. */
export function createNativeInstallationHost(host: Readonly<{platform: () => string; description: (raw: unknown) => Pick<InstallationIdentity, 'name' | 'appVersion'>}>) {
  let generation = 0;
  /** Start synchronously, before awaiting secure storage, so a newer restore retires every old read.
   * No identity is generated here, and disposal never clears the installation shared by credential users. */
  return function beginNativeInstallation(signal?: AbortSignal) {
    const owned = ++generation;
    let disposed = false, claim = installationIdentity();
    let invalidWarned = false;
    const current = () => {
      const now = installationIdentity(), valid = !disposed && !signal?.aborted && owned === generation && now === claim;
      // First-cause evidence, release builds too (E-mobile-01): a newer restore generation and a
      // rewritten identity are different bugs. This reports which one retired the claim, once,
      // and never changes the guard or the identity setter.
      if (!valid && !invalidWarned && !disposed && !signal?.aborted) {
        invalidWarned = true;
        try {
          console.warn('portico_installation_invalid', JSON.stringify({disposed, aborted: !!signal?.aborted, owned, latest: generation,
            sameReference: now === claim, sameId: now?.installationId === claim?.installationId, hasClaim: !!claim, hasNow: !!now}));
        } catch { /* passive: the guard's answer never depends on its log */ }
      }
      return valid;
    };
    const assertCurrent = () => {if (!current()) throw cancelled();};
    const dispose = () => {disposed = true;};
    let preparing: ReturnType<typeof prepare> | undefined;
    async function prepare() {
      assertCurrent();
      const saved = await readNativeSession(); assertCurrent();
      const id = saved.installation;
      if (!validInstallationId(id)) throw invalid();
      if (claim && claim.installationId !== id) throw mismatch();
      const env = nativeSelectedConnectionEnvironment();
      const connection = await env.storage.read(); assertCurrent();
      // A saved family may never be rebound to the current install after a reset or damaged read.
      for (const session of [saved.viewer?.session, connection?.session]) {
        if (session?.installationId !== undefined && session.installationId !== id) throw mismatch();
      }
      const metadata = host.description(await NativeModules.PorticoCapabilities.facts()); assertCurrent();
      const platform = host.platform();
      // Secure-record reset can happen while the native facts call is outstanding, even if the
      // canonical claim was initially absent. Its persisted identity must still be the one we read.
      const reread = await profileInstallation(true); assertCurrent();
      if (reread !== id) throw mismatch();
      setInstallationIdentity({installationId: id, ...metadata, platform, app: 'Portico'});
      claim = installationIdentity();
      if (!claim) throw invalid();
      return {saved, connection, env, identity: claim};
    }
    return {prepare: () => preparing ??= prepare(), current, dispose, generation: owned};
  };
}

import { NativeModules, Platform } from 'react-native';
import {
  inspectDirectServer,
  connectPairedServer,
  rememberNativeSession,
  forgetNativeSession,
  parseRememberedServer,
  type ConnectionEnvironment,
} from '../../../packages/client-core/src/server-connections';
import {
  RouteError,
  routeBase64,
  routeBytes,
  routeOrigin,
  type RouteCrypto,
  type ServerPin,
} from '../../../packages/client-core/src/route-identity';
import {
  directSignIn,
  selectDirectProfile,
  type ManagedProfile,
  type DirectSnapshot,
} from '../../../packages/client-core/src/profile-management';
import {
  ViewerSelectionCommit,
  selectedViewerRecord,
  type SelectedViewerRecord,
} from '../../../packages/client-core/src/viewer-selection';
import {
  installationIdentity,
  setInstallationIdentity,
  validInstallationId,
} from '../../../packages/client-core/src/installation';
import type {
  HttpLocalApi,
  LocalSession,
} from '../../../packages/client-core/src';
import { routePath } from '../../../packages/contracts/src/routes';
import { NativeQueueStore } from '../src-next/platform/queue-storage';
import {
  nativeProfileFetch,
  profileAPI,
} from '../src-next/platform/profile-transport';
import {
  nativeViewerStorage,
  profileInstallation,
} from '../src-next/platform/profile-trust';
import { prepareSecureRecords } from '../src-next/platform/secure-records';

// Separate setup entry only. Use the product native crypto/profile/secure-store
// bridges and canonical core protocols without importing the diagnostic shell.
const crypto: RouteCrypto = {
  random: size => {
    const bytes: unknown =
      NativeModules.PorticoRuntime.playbackRandomBytes(size);
    if (
      !Array.isArray(bytes) ||
      bytes.length !== size ||
      bytes.some(value => !Number.isInteger(value) || value < 0 || value > 255)
    )
      throw new RouteError(
        'random_unavailable',
        'Secure random source unavailable.',
      );
    return new Uint8Array(bytes);
  },
  sha256: async bytes =>
    routeBytes(
      await NativeModules.PorticoNetwork.sha256(routeBase64(bytes)),
      32,
    ),
  verify: async (key, signature, payload) =>
    !!(await NativeModules.PorticoNetwork.verify(
      routeBase64(key),
      routeBase64(signature),
      routeBase64(payload),
    )),
};
function environment(): ConnectionEnvironment {
  const store = new NativeQueueStore();
  return {
    fetcher: nativeProfileFetch,
    crypto,
    storage: {
      read: () =>
        store.change('network.selected.v1', raw => ({
          record: raw,
          value: parseRememberedServer(raw),
        })),
      change: update =>
        store.change('network.selected.v1', raw => ({
          record: update(parseRememberedServer(raw)),
          value: undefined,
        })),
    },
  };
}
const cancelled = () =>
  Object.assign(new Error('Setup cancelled.'), { name: 'AbortError' });
const refused = () =>
  new RouteError(
    'identity_mismatch',
    'The sign-in does not match this verified device and server.',
  );

/** Fresh-device-only sign-in adapter. It exposes no session/token/native record
 * and starts no Home, player, refresh feed, diagnostic sink or native observers. */
export class NativeSignInSetup {
  private readonly abort = new AbortController();
  private readonly env = environment();
  private readonly commit = new ViewerSelectionCommit(nativeViewerStorage);
  private readonly owned = new Map<string, LocalSession>();
  private readonly revocations = new Map<string, Promise<void>>();
  private address?: string;
  private pin?: ServerPin;
  private api?: HttpLocalApi;
  private snapshot?: DirectSnapshot;
  private account?: LocalSession;
  private selected?: SelectedViewerRecord;
  private installation?: ReturnType<typeof installationIdentity>;
  private ready = false;
  private busy = false;
  private complete = false;
  private cleanupFailed = false;
  private assertCurrent = () => {
    if (
      this.abort.signal.aborted ||
      (this.installation && installationIdentity() !== this.installation)
    )
      throw cancelled();
  };
  async prepare(): Promise<void> {
    this.assertCurrent();
    await prepareSecureRecords();
    this.assertCurrent();
    const id = await profileInstallation(true);
    this.assertCurrent();
    if (!validInstallationId(id)) throw refused();
    // Product storage remains private to this ordinary flow. Never replace an
    // existing saved viewer with this one-use fresh-installation setup adapter.
    if ((await nativeViewerStorage.read()) || (await this.env.storage.read()))
      throw new Error('A sign-in is already saved. Close setup.');
    const raw: unknown = await NativeModules.PorticoCapabilities.facts();
    this.assertCurrent();
    if (
      !raw ||
      typeof raw !== 'object' ||
      !('deviceName' in raw) ||
      typeof raw.deviceName !== 'string' ||
      !raw.deviceName.trim() ||
      !('appVersion' in raw) ||
      typeof raw.appVersion !== 'string' ||
      !/^[\x21-\x7e]{1,64}$/.test(raw.appVersion)
    )
      throw refused();
    if ((await profileInstallation(true)) !== id) throw refused();
    this.assertCurrent();
    const android = Platform.OS === 'android',
      amazon =
        android &&
        String(
          (Platform.constants as { Manufacturer?: string }).Manufacturer ?? '',
        ).toLowerCase() === 'amazon';
    if (!android && Platform.OS !== 'ios') throw refused();
    const platform = android
      ? Platform.isTV
        ? amazon
          ? 'firetv'
          : 'androidtv'
        : 'android'
      : Platform.isTV
      ? 'tvos'
      : (Platform as { isPad?: boolean }).isPad
      ? 'ipados'
      : 'ios';
    setInstallationIdentity({
      installationId: id,
      name: raw.deviceName,
      appVersion: raw.appVersion,
      app: 'Portico',
      platform,
    });
    this.installation = installationIdentity();
    this.ready = true;
  }
  private async run<T>(work: () => Promise<T>): Promise<T> {
    this.assertCurrent();
    if (!this.ready || this.busy || this.complete)
      throw new Error('Setup is not ready.');
    this.busy = true;
    try {
      return await work();
    } finally {
      this.busy = false;
    }
  }
  inspect(address: string): Promise<ServerPin> {
    return this.run(async () => {
      this.api?.getRouteConnection()?.dispose();
      this.api = undefined;
      this.pin = undefined;
      this.address = routeOrigin(address.trim());
      const pin = await inspectDirectServer(
        this.address,
        this.env,
        this.abort.signal,
      );
      this.assertCurrent();
      this.pin = pin;
      return pin;
    });
  }
  confirm(): Promise<void> {
    return this.run(async () => {
      if (!this.address || !this.pin) throw refused();
      const connected = await connectPairedServer(
        this.address,
        this.pin,
        this.env,
        this.abort.signal,
      );
      if (this.abort.signal.aborted) {
        connected.connection.dispose();
        throw cancelled();
      }
      this.assertCurrent();
      this.api = connected.api;
    });
  }
  signIn(
    username: string,
    password: string,
  ): Promise<readonly ManagedProfile[] | undefined> {
    return this.run(async () => {
      if (!this.api || !this.pin || !username || !password || this.account)
        throw new Error('Enter the sign-in details.');
      const api = this.api,
        pin = this.pin;
      const result = await directSignIn(
        api,
        username,
        password,
        this.abort.signal,
      );
      if (result.kind !== 'signed-in')
        throw new Error('This sign-in requires another step. Close setup.');
      return this.issued(result.session, async () => {
        this.assertCurrent();
        if (
          result.snapshot.serverId !== pin.serverId ||
          result.session.installationId !== this.installation?.installationId
        )
          throw refused();
        if (result.passwordChangeRequired)
          throw new Error(
            'This sign-in requires a password change. Close setup.',
          );
        api.setAccessToken(result.session.accessToken);
        if (result.accountScoped) {
          this.account = result.session;
          this.snapshot = result.snapshot;
          return result.snapshot.profiles;
        }
        await this.persist(result.session);
        return;
      });
    });
  }
  choose(profileId: string, pin?: string): Promise<void> {
    return this.run(async () => {
      const snapshot = this.snapshot;
      if (
        !this.api ||
        !snapshot ||
        !this.account ||
        !snapshot.profiles.some(profile => profile.id === profileId)
      )
        throw refused();
      const selected = await selectDirectProfile(
        this.api,
        {
          authority: 'local',
          accountId: snapshot.account.id,
          serverId: snapshot.serverId,
        },
        profileId,
        { pin, installationId: this.installation?.installationId },
        this.abort.signal,
      );
      await this.issued(selected.session, async () => {
        this.assertCurrent();
        await this.persist(selected.session);
      });
    });
  }
  private async issued<T>(
    session: LocalSession,
    work: () => Promise<T>,
  ): Promise<T> {
    this.owned.set(session.sessionFamilyId, session);
    try {
      return await work();
    } catch (failure) {
      this.dispose();
      // Publication transfers the selected viewer out of setup ownership.
      // A late unmount during temporary account logout must not revoke it.
      if (this.selected?.session.sessionFamilyId !== session.sessionFamilyId)
        await this.revoke(session).catch(() => {});
      throw failure;
    }
  }
  private revoke(session: LocalSession): Promise<void> {
    const pending = this.revocations.get(session.sessionFamilyId);
    if (pending) return pending;
    this.owned.delete(session.sessionFamilyId);
    const task = (async () => {
      if (!this.address) return;
      try {
        await profileAPI(this.address, session.accessToken).logout();
      } finally {
        await forgetNativeSession(this.env, session);
      }
    })();
    this.revocations.set(session.sessionFamilyId, task);
    return task;
  }
  private async persist(session: LocalSession): Promise<void> {
    const api = this.api;
    if (
      !api ||
      !this.address ||
      session.viewer.authority !== 'local' ||
      session.viewer.role === 'account' ||
      session.installationId !== this.installation?.installationId
    )
      throw refused();
    this.assertCurrent();
    await rememberNativeSession(
      api,
      session,
      this.env,
      undefined,
      () =>
        !this.abort.signal.aborted &&
        installationIdentity() === this.installation,
    );
    this.assertCurrent();
    await this.commit.commit(selectedViewerRecord(this.address, session), {
      current: () => this.selected ?? null,
      fence: () => {
        this.selected = undefined;
      },
      assertCurrent: this.assertCurrent,
      assertCandidateCurrent: this.assertCurrent,
      verify: (record, signal) =>
        profileAPI(record.serverUrl, record.session.accessToken).request(
          routePath('get_me'),
          'GET',
          undefined,
          signal,
        ),
      publish: record => {
        this.selected = record;
      },
      revoke: record => this.revoke(record.session),
      cleanupFailed: () => {
        this.cleanupFailed = true;
      },
    });
    this.owned.delete(session.sessionFamilyId);
    // The account-tier family has a separate lifetime from the selected viewer.
    if (this.account) {
      await this.revoke(this.account);
      this.account = undefined;
    }
    if (this.cleanupFailed)
      throw new Error('Sign-in cleanup could not be confirmed.');
    this.assertCurrent();
    this.complete = true;
  }
  dispose(): void {
    this.abort.abort();
    const connection = this.api?.getRouteConnection();
    // Session revocation still needs this verified route. Retire it only after
    // the bounded canonical logout requests finish, including rollback cleanup.
    for (const session of this.owned.values())
      this.revoke(session).catch(() => {});
    Promise.allSettled([...this.revocations.values()]).then(() =>
      connection?.dispose(),
    );
    this.account = undefined;
    this.snapshot = undefined;
  }
}

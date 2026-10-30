import type {ReactNode} from 'react';
import {NativeModules, Platform} from 'react-native';
import type {SettingsPlatform} from '../../../../../packages/client-core/src/presentation/settings-structure';
import {nativeAccounts} from '../../platform/accounts';
import {nativeRouteCrypto} from '../../platform/server-connections';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeShellRoute} from '../../shell/routes';
import type {NativeScreenLifetime} from './lifetime';
import type {NativeSettingsDownloads, NativeSettingsLivePorts} from './ports';
import {nativeSettingsContext} from './settings-context';
import {renderNativeSettingsPanel} from './settings-panels';
import {nativeTogetherSession} from './together-session';

/** The real UUID source the native runtime already uses for playback request ids. */
export function nativeSettingsRequestId(): string {
  const runtime = NativeModules.PorticoRuntime as undefined | {playbackRequestId?: () => string};
  if (typeof runtime?.playbackRequestId !== 'function') throw new Error('native_settings_request_id_unavailable');
  return runtime.playbackRequestId();
}

/** DVR's protocol wants 24 genuine random bytes as 48 lower-case hex characters. */
export async function nativeSettingsDvrRequestId(lifetime: NativeScreenLifetime): Promise<string> {
  lifetime.assert();
  return [...nativeRouteCrypto.random(24)].map(byte => byte.toString(16).padStart(2, '0')).join('');
}

export type NativeSettingsOpenRoute = (route: NativeShellRoute) => void;

export const nativeSettingsPlatform = (): SettingsPlatform => Platform.isTV ? 'tv' : 'phone';

/** One set of genuine ports for the Settings destination and the Dashboard route it opens. */
export function createNativeSettingsPorts(context: NativeShellBootstrapContext, lifetime: NativeScreenLifetime, openRoute: NativeSettingsOpenRoute, downloads?: NativeSettingsDownloads): NativeSettingsLivePorts {
  const open = (route: NativeShellRoute): void => {
    lifetime.assert();
    openRoute(route);
  };
  return Object.freeze({
    context,
    settingsContext: () => nativeSettingsContext(context, nativeSettingsPlatform(), !!downloads),
    serverName: () => context.serverName,
    requestId: nativeSettingsRequestId,
    dvrRequestId: () => nativeSettingsDvrRequestId(lifetime),
    together: nativeTogetherSession(context),
    accountActions: Object.freeze({
      signOut: async () => {
        const session = context.session();
        if (!session) throw Object.assign(new Error('native_settings_sign_out_without_session'), {code: 'session_ended'});
        await nativeAccounts.signOutServer(session.viewer.serverId, session.sessionFamilyId);
      },
      /** The genuine approval journey lives on the Servers screen; this row opens it. */
      linkTV: () => open({kind: 'servers'}),
    }),
    renderSettingsPanel: (id, runtime): ReactNode => renderNativeSettingsPanel(id, {context, lifetime, t: runtime.text.t, openRoute: open, downloads}, runtime),
    ...(downloads ? {downloads} : {}),
  });
}

import type {LocalSession} from '../../../../../packages/client-core/src/index';
import type {PreferenceSnapshot} from '../../../../../packages/client-core/src/preferences';
import type {DirectSnapshot} from '../../../../../packages/client-core/src/profile-management';
import type {SettingsContext, SettingsCustomId, SettingsPlatform} from '../../../../../packages/client-core/src/presentation/settings-structure';
import {playerSettingsSupport} from '../../platform/playback/native-listening-kinds';
import {nativeEqualizer} from '../../platform/playback/equalizer';
import {installationIdentity} from '../../../../../packages/client-core/src/installation';
import {NativeModules, Platform} from 'react-native';
import {nativeAccountSecurityAllowed, nativeAccessTokensAllowed} from './native-account-security';

export type NativeSettingsContextSource = Readonly<{
  account: DirectSnapshot;
  preferences: () => PreferenceSnapshot;
  session: () => LocalSession | undefined;
}>;

/**
 * The Settings structure's capabilities, from this viewer's real account and the server's
 * published preference keys. A capability is present only where this build draws the genuine
 * surface for it. Local credentials and server devices require the main profile's
 * account-management grant; a member's own access tokens need a signed-in local
 * account. Downloads storage is wired through the read-only device summary port.
 */
export function nativeSettingsContext(source: NativeSettingsContextSource, platform: SettingsPlatform = 'phone', downloads = false): SettingsContext {
  const account = source.account.account;
  const hosted = !!account.hostedAccountId;
  const preferenceKeys = new Set(source.preferences().registry.fields.map(field => field.key));
  return Object.freeze({
    owner: account.role === 'owner',
    administrator: account.role === 'admin',
    platform,
    capabilities: Object.freeze({
      preferenceKeys,
      localAccount: !hosted,
      porticoMember: hosted,
      hostedAccount: hosted,
      manageSignIn: nativeAccountSecurityAllowed(source, 'password'),
      manageDevices: nativeAccountSecurityAllowed(source, 'devices'),
      accessTokens: nativeAccessTokensAllowed(source),
      deviceName: !!installationIdentity()?.name,
      downloads,
      playbackSpeed: playerSettingsSupport.playbackSpeed,
      audioEffects: playerSettingsSupport.audioEffects,
      equalizer: nativeEqualizer.getSnapshot().supported,
      // Only the device whose engine actually has a rate-matching bridge offers the row.
      sampleRateMatching: Platform.OS === 'ios' && typeof NativeModules?.PorticoPlaybackModule?.setAudioSampleRateMatching === 'function',
    }),
  });
}

/** The account pages' custom rows this build draws, each from its own real reader or route. */
export const nativeAccountPanelIds: readonly SettingsCustomId[] = Object.freeze(['identity', 'profiles', 'password', 'twoStep', 'devices', 'servers', 'porticoAccount', 'accessTokens', 'about', 'privacyNote', 'remoteControls', 'deviceName', 'downloadsStorage']);

/** Drawn rows whose genuine native surface is not built yet; their capability stays off. */
export const nativeSettingsUnavailablePanelIds: readonly SettingsCustomId[] = Object.freeze(['subtitlePreview']);

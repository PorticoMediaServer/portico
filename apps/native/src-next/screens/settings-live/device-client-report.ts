import {Platform} from 'react-native';
import {BUILD_ID} from '../../platform/build-id';
import {sendClientReport} from '../../../../../packages/client-core/src/client-reports';
import {installationIdentity} from '../../../../../packages/client-core/src/installation';
import {forgetCrash, journal, lastCrash} from '../../platform/error-capture';
import {reportBody} from '../../platform/error-journal';
import type {NativeSettingsPanelDeps} from './settings-panels';

/** Explicitly requested, bounded/redacted diagnostics for this captured server only. */
export async function sendNativeDeviceReport({context, lifetime}: Pick<NativeSettingsPanelDeps, 'context' | 'lifetime'>, consent: Readonly<{signal: AbortSignal; current: () => boolean}>): Promise<void> {
  const assertConsent = () => {
    lifetime.assert();
    if (consent.signal.aborted || !consent.current()) throw Object.assign(new Error('native_device_report_consent_ended'), {name: 'AbortError'});
  };
  assertConsent();
  const identity = installationIdentity();
  if (!identity) throw new Error('native_device_report_installation_unavailable');
  const crash = await lastCrash();
  assertConsent();
  if (installationIdentity() !== identity) throw new Error('native_device_report_installation_changed');
  const deviceClass = Platform.isTV ? 'television' : 'mobile';
  // O-159: the report names the exact build as well as the store version.
  const body = reportBody({appVersion: `${identity.appVersion} (build ${BUILD_ID})`, platform /* lint-strings-allow: diagnostic report text */: identity.platform, deviceClass, deviceName: identity.name,
    now: Date.now(), fatal: crash, entries: journal.list()});
  await lifetime.request(signal => {
    assertConsent();
    if (installationIdentity() !== identity) throw new Error('native_device_report_installation_changed');
    return sendClientReport(context.api, lifetime.authority.serverId,
      {platform: identity.platform, deviceClass, appVersion: identity.appVersion, body}, signal);
  }, consent.signal);
  assertConsent();
  if (crash) await forgetCrash(crash);
}

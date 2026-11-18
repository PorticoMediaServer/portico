import {BUILD_ID} from '../../platform/build-id';
import {callServer} from '../../../../../packages/client-core/src/query/server-api';
import {installationIdentity} from '../../../../../packages/client-core/src/installation';
import {forgetCrash, journal, lastCrash} from '../../platform/error-capture';
import {reportBody} from '../../platform/error-journal';
import type {NativeSettingsPanelDeps} from './settings-panels';
import {nativeDeviceClass} from '../../platform/device-platform';

/** Explicitly requested, bounded/redacted diagnostics for this captured server only, sent as a report the owner reads in
 * Troubleshooting (`POST /v1/reports`, definition 12 rules 70 and 71): a crash report when the last run closed
 * unexpectedly, otherwise a problem report. The server asks a problem report to say what went wrong; these buttons have
 * no text box, so it carries one fixed line. */
const PROBLEM_LINE = 'Sent from this app with its recent warnings and errors.'; // lint-strings-allow: diagnostic report text, read by the owner

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
  const deviceClass = nativeDeviceClass;
  // O-159: the report names the exact build as well as the store version.
  const body = reportBody({appVersion: `${identity.appVersion} (build ${BUILD_ID})`, platform /* lint-strings-allow: diagnostic report text */: identity.platform, deviceClass,
    now: Date.now(), fatal: crash, entries: journal.list()});
  await lifetime.request(signal => {
    assertConsent();
    if (installationIdentity() !== identity) throw new Error('native_device_report_installation_changed');
    return callServer(context.api, 'POST /v1/reports', {signal, body: {kind: crash ? 'crash' : 'problem', ...(crash ? {} : {message: PROBLEM_LINE}),
      context: {app: identity.app, platform: identity.platform, appVersion: identity.appVersion, deviceClass}, log: body}});
  }, consent.signal);
  assertConsent();
  if (crash) await forgetCrash(crash);
}

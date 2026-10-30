import React, {useEffect, useRef, useState} from 'react';
import {AppState, NativeModules, Platform} from 'react-native';
import {appleProfile, type AppleNativeFacts} from '../../../../../packages/client-core/src/client-profile';
import {installationIdentity} from '../../../../../packages/client-core/src/installation';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {SettingsRow} from '../../kit/shell';

type Facts = Readonly<{eligible?: boolean; atmos: boolean}>;

/** Canonical Apple input only: the raw facts go through client-profile's own
 * appleProfile, and Atmos is claimed only when that normalised native engine
 * actually carries eac3 objectAudio 'atmos'. There is no video fallback, and a
 * missing value is not evidence. */
function readFacts(value: unknown): Facts | undefined {
  if (!value || typeof value !== 'object') return undefined;
  const facts = value as AppleNativeFacts;
  let atmos = false;
  try {
    const document = appleProfile({television: true, osVersion: String(Platform.Version), model: facts.model,
      appVersion: installationIdentity()?.appVersion}, facts);
    atmos = document.engines.native.audio?.some(audio => audio.codec === 'eac3' && (audio.objectAudio ?? []).includes('atmos')) === true;
  } catch {atmos = false;}
  return {eligible: typeof facts.audio?.eac3JocEligible === 'boolean' ? facts.audio.eac3JocEligible : undefined, atmos};
}

/** Frame 12's Audio passthrough is a capability line, not a control: the Apple engines
 * have no stored passthrough choice to honour. It reports canonical eligibility with
 * the mirrored copy: the full Dolby Digital Plus with Atmos line only when the
 * normalised engine vouches for Atmos, the existing Dolby Digital Plus name otherwise,
 * and nothing invented for an unsupported or unknown report. */
export function NativeApplePassthroughRow({testID}: {testID?: string}) {
  const t = defaultI18n.t, [facts, setFacts] = useState<Facts | null>();
  const generation = useRef(0);
  useEffect(() => {
    let alive = true;
    const load = async () => {
      const read = ++generation.current;
      try {
        const value = await (NativeModules.PorticoCapabilities?.facts?.() as Promise<unknown> | undefined);
        if (!alive || read !== generation.current) return;
        setFacts(readFacts(value) ?? null);
      } catch {
        if (alive && read === generation.current) setFacts(null);
      }
    };
    void load();
    const subscription = AppState.addEventListener('change', value => {if (value === 'active') void load();});
    return () => {alive = false; subscription.remove();};
  }, []);
  if (facts === undefined) return null;
  const value = facts?.atmos === true ? t('next.deviceCapabilities.applePassthrough')
    : facts?.eligible === true ? t('next.kit.codec.eac3')
    : facts?.eligible === false ? undefined
    : t('web.capabilities.unknown');
  return <SettingsRow testID={testID} label={t('next.settingsSpec.account.device.groups.1.rows.7.label')}
    description={t('next.deviceCapabilities.applePassthroughHint')} value={value} readable/>;
}

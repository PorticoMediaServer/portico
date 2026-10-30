import React, {useEffect, useRef, useState} from 'react';
import {AppState, NativeModules, Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {SettingsRow} from '../../kit/shell';
import {shellType} from '../../kit/shell/visual';
import {useTheme} from '../../kit/theme';

type Facts = Readonly<{hardware: readonly string[]; ranges: readonly string[]; width?: number; height?: number; rate?: number; channels?: number}>;
const codecNames: Readonly<Record<string, string>> = {avc: 'H.264', h264: 'H.264', hevc: 'HEVC', h265: 'HEVC', vp9: 'VP9', av1: 'AV1'};
const rangeNames: Readonly<Record<string, string>> = {hdr10: 'HDR10', hdr10plus: 'HDR10+', hlg: 'HLG', dolby_vision: 'Dolby Vision'};

/** Closed vocabulary only: a native token the app does not know is left out, never
 * printed raw. Screen size and rate are the producer's separate maxima; the audio
 * count is only the output channel count it reports. Reported numbers must be
 * integers (or a finite rate) inside the canonical limits, so no rounded fraction
 * ever becomes an output fact; anything else stays unknown. */
function readFacts(value: unknown): Facts | undefined {
  if (!value || typeof value !== 'object') return undefined;
  const facts = value as {decoders?: unknown; displayHdr?: unknown; screen?: {width?: unknown; height?: unknown; maxFrameRate?: unknown}; audio?: {maxOutputChannels?: unknown}};
  const boundedInt = (value: unknown, max: number) => typeof value === 'number' && Number.isInteger(value) && value >= 1 && value <= max ? value : undefined;
  const boundedRate = (value: unknown) => typeof value === 'number' && Number.isFinite(value) && value >= 1 && value <= 960 ? value : undefined;
  const decoders = Array.isArray(facts.decoders) ? facts.decoders : [];
  const hardware = [...new Set(decoders.flatMap(entry => {
    if (!entry || typeof entry !== 'object' || (entry as {hardware?: unknown}).hardware !== true) return [];
    const codec = (entry as {codec?: unknown}).codec;
    const name = typeof codec === 'string' ? codecNames[codec.toLowerCase()] : undefined;
    return name ? [name] : [];
  }))];
  const ranges = [...new Set((Array.isArray(facts.displayHdr) ? facts.displayHdr : []).flatMap(range => {
    const name = typeof range === 'string' ? rangeNames[range] : undefined;
    return name ? [name] : [];
  }))];
  return {hardware, ranges, width: boundedInt(facts.screen?.width, 32768), height: boundedInt(facts.screen?.height, 32768),
    rate: boundedRate(facts.screen?.maxFrameRate), channels: boundedInt(facts.audio?.maxOutputChannels, 16)};
}

/** Frames 10 and 11's Capabilities group, from the television's own report. Only
 * stated, known facts appear. The producer maximises resolution and refresh across
 * modes independently, so they are drawn as two qualified maxima rather than one
 * supported width × height at rate combination. */
export function NativeCapabilityRows({testID = 'settings-device-capabilities'}: {testID?: string}) {
  const theme = useTheme(), t = defaultI18n.t, [facts, setFacts] = useState<Facts>();
  const generation = useRef(0);
  useEffect(() => {
    let alive = true;
    const load = async () => {
      const read = ++generation.current;
      try {
        const value = await (NativeModules.PorticoCapabilities?.facts?.() as Promise<unknown> | undefined);
        if (!alive || read !== generation.current) return;
        setFacts(readFacts(value));
      } catch {
        if (alive && read === generation.current) setFacts(undefined);
      }
    };
    void load();
    const subscription = AppState.addEventListener('change', value => {if (value === 'active') void load();});
    return () => {alive = false; subscription.remove();};
  }, []);
  if (!facts) return null;
  const resolution = facts.width && facts.height ? t('next.deviceCapabilities.resolution', {width: facts.width, height: facts.height}) : undefined;
  const refresh = facts.rate ? t('next.deviceCapabilities.refreshRate', {rate: facts.rate}) : undefined;
  const screen = [facts.ranges.join(', '), resolution, refresh].filter((part): part is string => !!part).join(' · ');
  const rows = [
    ...(facts.hardware.length ? [{id: 'hardware', label: t('next.settingsSpec.account.device.advanced.groups.0.rows.0.label'), value: defaultI18n.list(facts.hardware, 'and')}] : []),
    ...(screen ? [{id: 'screen', label: t('next.settingsSpec.account.device.advanced.groups.0.rows.1.label'), value: screen}] : []),
    ...(facts.channels ? [{id: 'sound', label: t('next.settingsSpec.account.device.advanced.groups.0.rows.2.label'), value: t('next.deviceCapabilities.channels', {count: facts.channels})}] : []),
  ];
  if (!rows.length) return null;
  return <View testID={testID} style={{gap: 8, marginBottom: 30}}>
    <Text style={[shellType(20, 700), {color: theme.colors.textTertiary}]}>{t('next.settingsSpec.account.device.advanced.groups.0.title')}</Text>
    {rows.map(row => <SettingsRow key={row.id} testID={`${testID}-${row.id}`} label={row.label} value={row.value} readable/>)}
  </View>;
}

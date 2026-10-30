import type {LibraryChannel, LibraryChannelPreview} from '../../../../../../../packages/client-core/src/library-channels';
import {defaultI18n, type MessageId} from '../../../../../../../packages/i18n/src';

export type ChannelTone = 'healthy' | 'building' | 'warning' | 'danger' | 'neutral';
/** The states the server stores for a channel; a problem it reports through healthCode is
 * never drawn as on air, and neither is a state this build does not know. */
const STATES: Readonly<Record<string, Readonly<{label: MessageId; tone: ChannelTone}>>> = {
  healthy: {label: 'server.channels.state.ready', tone: 'healthy'},
  generating: {label: 'server.channels.state.generating', tone: 'building'},
  stale: {label: 'server.channels.state.stale', tone: 'warning'},
  disabled: {label: 'server.channels.state.disabled', tone: 'neutral'},
  'no-eligible-media': {label: 'server.channels.state.degraded', tone: 'danger'},
  'unresolved-duration': {label: 'server.channels.state.degraded', tone: 'danger'},
};
export function channelStatus(channel: Pick<LibraryChannel, 'state' | 'healthCode'>): Readonly<{label: MessageId; tone: ChannelTone}> {
  const known = STATES[channel.state];
  return known && !channel.healthCode ? known : {label: 'server.channels.state.degraded', tone: 'danger'};
}
export function previewMatches(eligible: number, complete: boolean, t: typeof defaultI18n.t): string {
  return complete ? t('builder.matches', {count: eligible}) : `At least ${defaultI18n.number(eligible)} ${eligible === 1 ? 'title matches' : 'titles match'}`;
}
export function previewDuration(preview: Pick<LibraryChannelPreview, 'durationMs' | 'complete' | 'rules'>): string {
  return `${preview.complete && preview.rules.every(rule => rule.complete) ? '≈' : '≥'} ${defaultI18n.number(preview.durationMs / 3600000, {minimumFractionDigits: 1, maximumFractionDigits: 1})} h`;
}

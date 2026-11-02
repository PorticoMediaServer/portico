import type {MessageId} from '../../../i18n/src/index.ts';
import {unavailableIsServerWide} from '../channel-guide.ts';
import {legacyGuideState} from '../guide/legacy-source.ts';

/**
 * Spec §10 "Channel unavailable": the catalogue sentence for a channel's own `tuneUnavailableReason`
 * (LT-4). A reason about the server's setup (`unavailableIsServerWide`) is not repeated per channel;
 * the guide shows it once (`liveServerWideNotice`). Unknown reasons read as "can't play".
 */
export function channelUnavailableMessage(reason: string): MessageId | undefined {
  if (!reason || unavailableIsServerWide(reason)) return undefined;
  switch (reason) {
    case 'source-disabled': return 'web.live.reason.sourceOff';
    case 'source-unavailable': return 'web.live.reason.sourceDown';
    case 'capacity-unavailable': return 'web.live.reason.tunersBusy';
    case 'permission-denied': return 'web.live.reason.permission';
    case 'schedule_preparing': return 'web.live.reason.preparing';
    case 'no-schedule': return 'web.live.reason.noSchedule';
    case 'not-recordable': return 'web.live.reason.notRecordable';
  }
  return 'web.live.reason.cantPlay';
}

type SourceFacts = Readonly<{channelCount: number; unavailableReasons: Readonly<Record<string, number>>; refreshState: string}>;

/** The one guide notice for a server-wide reason any shown source publishes for its channels
 *  (the summaries count reasons; nothing is inferred from the loaded page). Owners get the cause;
 *  members get the plain sentence. */
export function liveServerWideNotice(sources: readonly SourceFacts[], owner: boolean): Readonly<{title: MessageId; body: MessageId}> | undefined {
  const reason = sources.flatMap(source => Object.entries(source.unavailableReasons).filter(([value, count]) => count > 0 && unavailableIsServerWide(value)).map(([value]) => value))[0];
  if (!reason) return undefined;
  const body: MessageId = !owner ? 'web.live.serverWide.memberBody'
    : reason === 'decoder_confinement_unavailable' ? 'web.live.serverWide.platformBody'
      : reason === 'ffprobe_not_configured' || reason === 'ffmpeg_not_configured' || reason === 'decoder_dependencies_unavailable' ? 'web.live.serverWide.toolsBody'
        : 'web.live.serverWide.deliveryBody';
  return {title: 'web.live.serverWide.title', body};
}

/** Spec §10 "Guide stale": a shown source whose last refresh failed (LT-3). The server's own
 *  refresh state decides it, never a count of empty days. */
export function liveGuideStale(sources: readonly SourceFacts[]): boolean {
  return sources.some(source => legacyGuideState(source.refreshState) === 'stale');
}

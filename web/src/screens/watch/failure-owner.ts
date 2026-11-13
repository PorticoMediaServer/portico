import type {I18n} from '@i18n';
import type {WebPlaybackRuntimeSnapshot} from '../../platform/playback';
import type {PlaybackFailureOwner} from '../../kit/states/PlaybackFailure';
import {browserLabel} from '../../shell/browser-label';
import {watchPlaybackInfo} from './info-copy';
import {watchClock} from './model';

/** The owner's part of a playback failure (states.html frames 8 and 10): what this browser did, how the title was being
 * sent, the server's code and when. Only for the owner of a server they manage; members see the failure without it. */
const code = (value: string | undefined) => value && /^[a-z0-9_]{1,64}$/.test(value) ? [value] : [];
function owner(paragraphs: readonly string[], codes: readonly string[], i18n: I18n): PlaybackFailureOwner {
  const time = i18n.time(new Date());
  return Object.freeze({paragraphs, codes, time, copyText: [...paragraphs, ...codes.map(value => `${i18n.t('next.states.code', {count: 1})} ${value}`), time].join('\n')});
}

/** A playback that stopped in the player: where, and the route its picture took, from the session's own playback info. */
export function playingFailureOwner(snapshot: WebPlaybackRuntimeSnapshot, i18n: I18n, title: string): PlaybackFailureOwner {
  const paragraphs = [i18n.t('next.states.ownerStopped', {browser: browserLabel(), position: watchClock(snapshot.playback.positionSeconds)})];
  const info = snapshot.session?.playbackInfo ? watchPlaybackInfo(snapshot.session.playbackInfo, snapshot.session.observedPlayback, i18n, title) : undefined;
  const picture = info?.sections.find(section => section.kind === 'picture');
  if (picture) {
    const value = (field: string) => {const row = picture.rows.find(item => item.field === field); return picture.route === 'converted' ? row?.delivered ?? row?.source : row?.source ?? row?.delivered;};
    const format = [value('codec'), value('resolution')].filter(Boolean).join(', ');
    if (format) paragraphs.push(i18n.t(picture.route === 'converted' ? 'next.states.ownerSentConverted' : picture.route === 'repackaged' ? 'next.states.ownerSentRepackaged' : 'next.states.ownerSentOriginal', {format}));
  }
  return owner(paragraphs, code(snapshot.playback.error), i18n);
}

/** A start the server refused before the first frame (the title page's card). */
export function startFailureOwner(failureCode: string, i18n: I18n): PlaybackFailureOwner {
  return owner([i18n.t('next.states.ownerStartRefused', {browser: browserLabel()})], code(failureCode), i18n);
}

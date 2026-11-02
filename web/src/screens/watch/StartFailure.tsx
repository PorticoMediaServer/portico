import {useShellFacts} from '../../shell/fact-hooks';
import {useLayoutEffect, useRef} from 'react';
import {defaultI18n} from '@i18n';
import {playbackUserMessage} from '@core/playback-messages.ts';
import type {WebPlaybackStartFailure} from '../../platform/playback/start-failure';
import type {WebPlaybackStartTimedOut} from '../../platform/playback/types';
import type {WebPlaybackAuthority} from '../../platform/playback/types';
import type {WatchPlaybackContext} from './play-on-binding';
import {router} from '../../router';
import {PlaybackFailure} from '../../kit/states/PlaybackFailure';
import {WatchStreamLimitSessions} from './StreamLimitSessions';
import compact from './start-problem.module.css';

const t = defaultI18n.t;
/** A refused start has no admitted fence/presentation. Its original HTTP owner is
 * the complete command authority; this view never looks up a later runtime. */
export function WatchStartFailure({failure, authority, context, reportError}: {failure: WebPlaybackStartFailure; authority: WebPlaybackAuthority; context?: WatchPlaybackContext; reportError: (error: unknown) => void}) {
  useShellFacts(context);
  const root = useRef<HTMLDivElement>(null), restore = useRef<HTMLElement | null>(null);
  useLayoutEffect(() => {
    restore.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    root.current?.querySelector<HTMLButtonElement>('button')?.focus({preventScroll: true});
    return () => {if (restore.current?.isConnected) restore.current.focus({preventScroll: true});};
  }, [failure]);
  if (!failure.isCurrent()) return null;
  const mayConfigure = () => {
    if (!failure.isCurrent() || !context?.isCurrent() || context.epoch !== authority.epoch) return false;
    const viewer = context.session().viewer, identity = context.facts?.identity;
    return viewer.serverId === authority.serverId && viewer.authority === authority.authority && viewer.accountId === authority.accountId && viewer.profileId === authority.profileId
      && viewer.authority === 'local' && viewer.role === 'owner' && identity?.primaryProfile === true && identity.capabilities?.manageServer === true;
  };
  const configure = ['transcode_not_allowed', 'transcoding_disabled'].includes(failure.code) && mayConfigure() ? () => {
    if (!mayConfigure()) return;
    failure.cancel();void router.navigate({to: '/settings', search: {scope: 'server', section: 'streaming'}}).catch(reportError);
  } : undefined;
  const limit = failure.streamLimit;
  const sentence = failure.code === 'stream_limit_reached'
    ? limit ? t('next.states.streamLimitBody', {count: limit.limit})
      : playbackUserMessage(failure.scope === 'account' ? 'owner_account_cap' : failure.scope === 'server' ? 'owner_server_cap' : undefined)
    : undefined;
  // J-27: a start refused before its first frame is a compact card where the person was, never a full screen.
  return <div ref={root} className={compact.card} role="alertdialog" aria-label={t('next.states.playbackStopped')}
    onKeyDown={event => {
      if (event.key === 'Escape') {event.preventDefault();failure.cancel();}
      if (event.key === 'Tab') {
        const buttons = Array.from(root.current?.querySelectorAll<HTMLButtonElement>('button:not(:disabled)') ?? []);
        const at = buttons.indexOf(document.activeElement as HTMLButtonElement);
        if (buttons.length && (event.shiftKey ? at <= 0 : at < 0 || at === buttons.length - 1)) {
          event.preventDefault();buttons[event.shiftKey ? buttons.length - 1 : 0]?.focus();
        }
      }
    }}>
    <PlaybackFailure layout="compact" error={failure.code} heading={limit ? t('next.states.streamLimit', {count: limit.playing}) : undefined} sentence={sentence}
      onRetry={failure.retryable && (failure.scope !== 'account' || failure.automaticRecoveryExhausted) ? failure.retry : undefined} onClose={configure ?? failure.cancel}
      closeLabel={configure ? t('web.player.error.turnOnConverting') : t('action.close')} onError={reportError}>
      {failure.scope === 'account' && <WatchStreamLimitSessions failure={failure} isCurrent={failure.isCurrent} reportError={reportError}/>}
    </PlaybackFailure>
  </div>;
}

/** O-159 and J-17: a start that ended before it could play, drawn inside the player (states.html
 * f8): the title's picture, frozen and dimmed, the plain reason, Try again and Back. The deadline
 * case keeps its own words; a refused start names what failed through the playback vocabulary. */
export function WatchStartTimedOut({timedOut, reportError}: {timedOut: WebPlaybackStartTimedOut; reportError: (error: unknown) => void}) {
  const root = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {root.current?.querySelector<HTMLButtonElement>('button')?.focus({preventScroll: true});}, [timedOut]);
  const deadline = timedOut.code === undefined;
  return <div ref={root} className={compact.card} data-start-problem={timedOut.code ?? 'playback_start_timeout'} role="alertdialog" aria-label={t(deadline ? 'web.player.error.startTitle' : 'next.states.playbackStopped')}
    onKeyDown={event => {if (event.key === 'Escape') {event.preventDefault();timedOut.close();}}}>
    <PlaybackFailure layout="compact" error={timedOut.code ?? 'playback_start_timeout'} heading={deadline ? t('web.player.error.startTitle') : undefined} sentence={deadline ? t('web.player.error.startBody') : undefined}
      onRetry={timedOut.retry} onClose={timedOut.close} closeLabel={t('action.close')} onError={reportError}/>
  </div>;
}

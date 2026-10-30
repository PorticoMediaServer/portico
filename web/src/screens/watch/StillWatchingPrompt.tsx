import {MetadataFacts} from '../../kit/MetadataFacts';
import {useLayoutEffect, useRef, useState} from 'react';
import {defaultI18n} from '@i18n';
import type {WebPlaybackRuntimeSnapshot} from '../../platform/playback';
import {Button} from '../../kit/controls/Button';
import {Dialog} from '../../kit/overlays';
import {watchStillWatching, type WatchStillWatchingBinding, type WatchStillWatchingOffer} from './still-watching';
import type {WatchBindings} from './types';
import styles from './still-watching.module.css';

type Action = 'continue' | 'replay' | 'stop';
/** This layout's own opening. Only the cleanup that owns it may retire it, and only
 * this exact object may take a click or settle an action. */
type Opening = {
  readonly tuple: string;
  readonly offer: WatchStillWatchingOffer;
  live: boolean;
  spent: boolean;
  op?: Readonly<{kind: Action}>;
};
type Pending = Readonly<{opening: Opening; op: Readonly<{kind: Action}>}>;

/** phone-player f6e. One committed opening for one exact held occurrence; the kit's
 * modal owns the scrim, focus trap and input, so the live chrome underneath is neither
 * visible through it nor able to take a click or shortcut. */
export function WatchStillWatchingPrompt({binding, snapshot, environment, expanded, layout}: {
  binding: WatchStillWatchingBinding; snapshot: WebPlaybackRuntimeSnapshot; environment: WatchBindings['environment'];
  expanded: boolean; layout: 'desktop' | 'tablet' | 'phone';
}) {
  const offer = expanded ? watchStillWatching(binding, snapshot) : null;
  // A layout replay redraws the dialog from the same tuple, never from retained facts.
  const tuple = offer ? `${offer.key}\u0000${layout}` : '';
  const openingRef = useRef<Opening | null>(null);
  const [opening, setOpening] = useState<Opening | null>(null);
  const [pending, setPending] = useState<Pending | null>(null);
  // A refused action re-admits through this layout rather than re-arming its own spent object.
  const [generation, setGeneration] = useState(0);
  // The admitted offer belongs to the tuple that keyed it; the committed pass below
  // is its only source and a later observation tick cannot replace it.
  const admittedOffer = useRef(offer);
  useLayoutEffect(() => {admittedOffer.current = offer;});
  useLayoutEffect(() => {
    const offer = admittedOffer.current;
    if (!offer || !tuple) {
      const old = openingRef.current;
      if (old) {old.live = false;openingRef.current = null;setOpening(current => current === old ? null : current);}
      return;
    }
    // Every setup owns a fresh object, including a Strict replay of the same tuple.
    const fresh: Opening = {tuple, offer, live: true, spent: false};
    openingRef.current = fresh;
    setOpening(fresh);
    return () => {
      fresh.live = false;
      if (openingRef.current === fresh) openingRef.current = null;
      setOpening(current => current === fresh ? null : current);
      setPending(current => current?.opening === fresh ? null : current);
    };
    // The offer is captured from this committed render on purpose: the stable tuple is
    // the only key, so an observation tick can never rebuild the opening.
  }, [binding, environment, tuple, generation]);
  const drawn = opening && opening.live && openingRef.current === opening && opening.tuple === tuple ? opening : null;
  if (!drawn) return null;
  const {next} = drawn.offer, t = defaultI18n.t, busy = drawn.spent || pending?.opening === drawn;
  const episode = next.episode ? environment.t('next.kit.episodeCode', {season: next.episode.seasonNumber, episode: next.episode.number}) : undefined;
  const act = (kind: Action) => {
    const held = openingRef.current;
    // The click speaks for this rendered opening only.
    if (!held || !held.live || held !== opening || held !== drawn || held.spent || held.op) return;
    // Repeat the whole predicate at the click, including this command's own admission.
    const now = watchStillWatching(binding, snapshot);
    if (!now || now.key !== held.offer.key || kind === 'replay' && !now.replayOffered) return;
    const op = Object.freeze({kind});
    held.spent = true;held.op = op;setPending({opening: held, op});
    const settle = (error?: unknown) => {
      if (held.op !== op || !held.live || openingRef.current !== held) return;
      // The ref can outlive a runtime replacement until the next commit: re-read the
      // whole predicate before any report or state.
      const live = watchStillWatching(binding, snapshot);
      if (!live || live.key !== held.offer.key) return;
      setPending(current => current?.opening === held && current.op === op ? null : current);
      const aborted = error instanceof DOMException && error.name === 'AbortError';
      if (error !== undefined && !aborted) binding.reportError(error);
      // A refused or cancelled action leaves the same real attention. Publish a fresh,
      // unspent opening so Close and Replay keep working; this object stays spent.
      setGeneration(value => value + 1);
    };
    const command = op.kind === 'continue' ? binding.policyAction(held.offer.fence, {kind: 'continue', nextEntryId: held.offer.next.entryId})
      : op.kind === 'replay' ? binding.replay(held.offer.fence) : binding.stop(held.offer.fence);
    void command.then(() => settle(), settle);
  };
  return <Dialog open onOpenChange={() => {}} title={t('player.post.stillWatching')} responsive={false} width={400}
    showCloseButton={false} dismissOnOutsidePress={false} dismissOnBack={false}
    className={styles.dialog} headerClassName={styles.header} bodyClassName={styles.body} footerClassName={styles.footer}
    footer={<>
      <Button variant="primary" size="small" disabled={busy} onClick={() => act('continue')}>{t('player.post.stillWatchingAction')}</Button>
      {drawn.offer.replayOffered && <Button variant="glass" size="small" icon="i-restart" disabled={busy} onClick={() => act('replay')}>{t('player.post.replay')}</Button>}
      <Button variant="glass" size="small" disabled={busy} onClick={() => act('stop')}>{t('player.post.close')}</Button>
    </>}>
    <p className={styles.next}><MetadataFacts parts={[t('player.post.upNext'), episode && <b>{episode}</b>, <b>{next.title}</b>]} /></p>
    <p>{t('player.post.pausedAfterEpisodes')}</p>
  </Dialog>;
}

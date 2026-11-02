import {useEffect, useMemo, useSyncExternalStore, type ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import {titleMenuGroups} from '@core/presentation/title-menu.ts';
import {Button, Select} from '../kit/controls';
import {Dialog, Menu} from '../kit/overlays';
import type {ScreenContext} from '../screens/detail/context';
import {titleMenuEntries} from './title-menu-entries';
import {TitleMenuHost, type TitleRatingPhase} from './title-menu-host';

export {menuIcons, titleMenuEntries, type TitleMenuEntryOptions} from './title-menu-entries';
export type {TitleMenuSubject} from './title-menu-host';

const t = defaultI18n.t;

export type RatingDialogProps = Readonly<{
  title: string;
  phase: TitleRatingPhase;
  /** The chosen rating, 0-10, or null for none; absent until the title's own rating is read. */
  value?: number | null;
  error?: string;
  returnFocusRef: React.RefObject<HTMLElement | null>;
  onValue: (value: number | null) => void;
  onSave: () => void;
  onRetry: () => void;
  onClose: () => void;
}>;
/** The rating dialog (Home's and every other title menu's): the title's own 0-10 rating, saved through its Detail. */
export function RatingDialog({title, phase, value, error, returnFocusRef, onValue, onSave, onRetry, onClose}: RatingDialogProps) {
  const busy = phase === 'loading' || phase === 'saving', ready = phase === 'ready';
  return <Dialog open onOpenChange={open => { if (!open && !busy) onClose(); }} title={t('title.rate', {title})}
    returnFocusRef={returnFocusRef} dismissOnOutsidePress={!busy} dismissOnBack={!busy} footer={<>
      <Button onClick={onClose} disabled={busy}>{t('action.close')}</Button>
      {ready && <Button variant="primary" onClick={onSave}>{t('action.save')}</Button>}
      {(phase === 'uncertain' || phase === 'error') && <Button variant="primary" onClick={onRetry}>{t('next.states.tryAgain')}</Button>}
    </>}>
    {value !== undefined && <Select label={t('next.home.yourRating')} aria-label={t('next.home.yourRating')} value={value === null ? 'none' : String(value)}
      options={[{value: 'none', label: t('next.home.noRating')}, ...Array.from({length: 11}, (_, stars) => ({value: String(stars), label: t('next.home.ratingValue', {value: stars})}))]}
      disabled={!ready} onValueChange={next => onValue(next === 'none' ? null : Number(next))} />}
    {busy && <p role="status">{t('next.selection.checking')}</p>}
    {phase === 'result' && <p role="status">{t('next.selection.saved')}</p>}
    {error && <p role="alert">{error}</p>}
  </Dialog>;
}

/**
 * The one title action menu for every screen that is not Home (FX-S3, FX-S4): `open` at a card's ⋯ or hold, `menu`
 * wherever the screen draws overlays, `play` for a card with no play of its own. The fencing and the actions live in
 * `TitleMenuHost`; this draws what it says is live.
 */
export function useTitleMenu(context: ScreenContext) {
  const host = useMemo(() => new TitleMenuHost(context), [context]);
  useSyncExternalStore(host.subscribe, host.getSnapshot, host.getSnapshot);
  // The screen's context, or this host leaving, retires everything it holds.
  useEffect(() => () => host.dispose(), [host]);
  // A screen that moves on while the menu is open takes the menu with it.
  useEffect(() => { host.sweep(); });
  const {menu, rating} = host.live();
  const entries = menu ? titleMenuEntries(titleMenuGroups(menu.caps), {
    rating: menu.caps.rating, loading: menu.phase === 'loading', onSelect: id => { void host.choose(menu, id); },
    ...(menu.subject.select ? {select: () => host.select(menu)} : {}),
  }) : [];
  const element: ReactNode = <>
    <Menu open={!!menu} onOpenChange={value => { if (!value && menu) host.close(menu); }} title={menu?.subject.title ?? ''} anchorRef={host.anchor} width={300} items={entries} />
    {rating && <RatingDialog title={rating.subject.title} phase={rating.phase} value={rating.value} error={rating.error} returnFocusRef={host.anchor}
      onClose={() => host.closeRating()} onValue={value => host.setRatingValue(rating, value)}
      onSave={() => { void host.saveRating(rating); }} onRetry={() => { void host.retryRating(rating); }} />}
  </>;
  return {open: host.show, menu: element, play: host.play};
}

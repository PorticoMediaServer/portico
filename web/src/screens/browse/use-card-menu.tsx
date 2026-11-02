import {useCallback} from 'react';
import type {ContentEntry} from '@core/library-content.ts';
import type {CardSelectionIntent} from '../../kit/cards';
import type {ScreenContext} from '../detail/context';
import {useTitleMenu} from '../../shell/title-menu';
import type {BrowseItemActions} from './EntryCard';

/** The minimum a menu needs from its surface: the applied query revision it is
 * drawing under, and whether the card's entry still belongs to that page. */
export type CardMenuAdmission = Readonly<{getSnapshot(): Readonly<{queryRevision: number}>; contains(entry: ContentEntry): boolean}>;
export type CardMenuInput = Readonly<{admission: CardMenuAdmission; queryRevision: number; context: ScreenContext}>;

/** The drawn card menu (select.html 17): its ⋯ button or a hold on an unselected card opens the one
 * shared title menu at that exact card (FX-S3) - the same actions Home offers, with Select when the card
 * belongs to the surface's explicit selection. The opening is bound to the card, its entry, the admission it
 * was drawn from and that admission's query revision; a new applied query retires it at once. Everything
 * here is presentation over the surface's own fenced actions. */
export function useLibraryCardMenu(input: CardMenuInput) {
  const {admission, queryRevision, context} = input;
  const {open: openTitleMenu, menu} = useTitleMenu(context);
  const open = useCallback((anchor: HTMLElement, base: BrowseItemActions, entry: ContentEntry, title: string) => {
    const descriptor = base.selection;
    openTitleMenu(anchor, {entry, title,
      current: () => admission.getSnapshot().queryRevision === queryRevision && admission.contains(entry),
      ...(base.onPlay ? {onPlay: base.onPlay} : {}), ...(descriptor ? {select: () => descriptor.onToggle({range: false, source: 'pointer'})} : {})});
  }, [admission, queryRevision, openTitleMenu]);
  /** Wrap one card's own fenced actions with this menu's ⋯ and hold. */
  const wrap = useCallback((base: BrowseItemActions, entry: ContentEntry): BrowseItemActions => {
    const title = entry.displayTitle ?? entry.title, descriptor = base.selection;
    // An owner that already supplies its own menu keeps it.
    const own = (anchor: HTMLButtonElement) => open(anchor, base, entry, title);
    const more = base.onMore ?? own;
    return {...base, onMore: more,
      ...(descriptor ? {selection: {...descriptor, onToggle: (intent: CardSelectionIntent) => {
        // A hold opens the menu only while this card is not selecting; once the
        // surface is selecting, the card keeps its drawn toggle gesture.
        if (intent.source === 'long-press' && intent.anchor && descriptor.active === false) {
          if (base.onMore) base.onMore(intent.anchor as HTMLButtonElement);
          else open(intent.anchor, base, entry, title);
          return;
        }
        descriptor.onToggle(intent);
      }}} : {})};
  }, [open]);
  return {wrap, menu};
}

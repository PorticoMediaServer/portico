import {useCallback, useLayoutEffect, useRef, useState} from 'react';
import type {SavedReadingGroupCard, ReadingSavedReferenceCommand} from '@core/reading-saved-group.ts';
import type {SavedService, SavedRoute} from '@core/saved.ts';
import type {ShellArtwork} from '../../shell/artwork';
import {defaultI18n} from '@i18n';
import {PosterCard} from '../../kit/cards';
import {MenuRow, Popover} from '../../kit/overlays';
import {StateView} from '../../kit/states';
import type {SavedRuntime} from './runtime';
import type {SavedOperations} from './operations';

type Opening = {card: SavedReadingGroupCard; service: SavedService; runtime: SavedRuntime; operations: SavedOperations; route: SavedRoute | null; controller: AbortController; live: boolean; busy: boolean; command?: ReadingSavedReferenceCommand; needsRefresh: boolean};
type View = Readonly<{opening?: Opening; busy?: boolean; failed?: boolean}>;
const {t} = defaultI18n;
const definite = new Set(['personal_state_conflict', 'operation_expired', 'idempotency_key_reused', 'invalid_request', 'not_found', 'not_permitted', 'unauthorized']);

/** A Saved-only reference card never participates in catalogue selection or
 * generic item effects. Unavailable cards carry no target to reconstruct. */
export function ReadingGroupCard({card, service, runtime, operations, artwork, width, open, reportError}: {
  card: SavedReadingGroupCard; service: SavedService; runtime: SavedRuntime; operations: SavedOperations; artwork: ShellArtwork; width: number;
  open?: (card: Extract<SavedReadingGroupCard, {hidden: false}>) => void; reportError: (error: unknown) => void;
}) {
  const inputs = useRef({card, service, runtime, operations, open, reportError});inputs.current = {card, service, runtime, operations, open, reportError};
  const held = useRef<Opening | undefined>(undefined), [view, setView] = useState<View>({}), currentView = useRef(view), [menu, setMenu] = useState(false), anchor = useRef<HTMLButtonElement>(null);
  const publish = useCallback((next: View) => {currentView.current = next;setView(next);}, []);
  const current = useCallback((opening: Opening) => opening.live && held.current === opening && !opening.controller.signal.aborted
    && inputs.current.card === opening.card && inputs.current.service === opening.service && inputs.current.runtime === opening.runtime && inputs.current.operations === opening.operations && opening.operations.current
    && opening.route?.view === 'watchlist' && opening.service.getSnapshot().route === opening.route
    && opening.service.getSnapshot().projection?.sections.some(section => section.entries.some(entry => entry === opening.card)) === true, []);
  useLayoutEffect(() => {
    const opening: Opening = {card, service, runtime, operations, route: service.getSnapshot().route, controller: new AbortController(), live: true, busy: false, needsRefresh: false};
    held.current = opening;publish({opening});setMenu(false);
    return () => {opening.live = false;opening.controller.abort();if (held.current === opening) held.current = undefined;};
  }, [card, service, runtime, operations, publish]);
  const opening = view.opening, renderedView = view, renderedCommand = opening?.command, renderedRefresh = opening?.needsRefresh;
  const remove = async () => {
    if (!opening || !current(opening) || opening.busy || currentView.current !== renderedView || opening.command !== renderedCommand || opening.needsRefresh !== renderedRefresh
      || (!renderedRefresh && opening.service.getSnapshot().phase !== 'ready')
      || !opening.card.removeMyList || !inputs.current.runtime.canRemoveReadingGroup(opening.card.id)) return;
    const command = renderedCommand ?? Object.freeze({operationId: crypto.randomUUID(), expectedRevision: opening.card.revision});
    opening.busy = true;publish({opening, busy: true});
    try {
      if (!renderedRefresh) {
        opening.command = command;
        await opening.runtime.removeReadingGroup(opening.card.id, command, opening.controller.signal);
        if (!current(opening) || opening.command !== command) return;
        opening.command = undefined;opening.needsRefresh = true;
      }
      // Receipt confirms only the original removal. A separate current Saved
      // read decides which cards remain; no optimistic target/member mutation.
      await opening.service.refresh();
      if (!current(opening)) return;
      // SavedService publishes read failures instead of rejecting refresh().
      // Keep the known-receipt read-only retry until a real ready page arrives.
      if (opening.service.getSnapshot().phase !== 'ready') {publish({opening, failed: true});return;}
      opening.needsRefresh = false;publish({opening});
    } catch (error) {
      if (current(opening)) {
        const code = typeof error === 'object' && error !== null && 'code' in error ? error.code : undefined;
        if (typeof code === 'string' && definite.has(code)) {opening.command = undefined;opening.needsRefresh = true;}
        publish({opening, failed: true});inputs.current.reportError(error);
      }
    } finally {opening.busy = false;}
  };
  const offered = card.removeMyList && runtime.canRemoveReadingGroup(card.id), label = t(view.failed ? 'next.states.tryAgain' : 'next.home.removeMyList');
  if (card.hidden) return <StateView title={t('card.unavailable')} primary={offered ? {id: 'remove-reference', label, onPress: () => {void remove();}, disabled: view.busy} : undefined}/>;
  return <><PosterCard model={{title: card.title, caption: [{id: 'count', role: 'value', text: t('web.saved.items', {count: card.count})}]}}
    artwork={artwork.foreground(card.posterUrl, width * devicePixelRatio, 2 / 3)} context="grid" onNavigate={() => {if (opening && current(opening) && !opening.busy) inputs.current.open?.(card);}}
    onMore={offered ? button => {if (opening && current(opening)) {anchor.current = button;setMenu(true);}} : undefined} moreExpanded={menu}/>
    {offered && <Popover open={menu} onOpenChange={setMenu} anchorRef={anchor} title={card.title} accessibilityLabel={t('next.cards.more')} role="menu" width={260}>
      <MenuRow disabled={view.busy} onClick={() => {setMenu(false);void remove();}}>{label}</MenuRow>
    </Popover>}</>;
}

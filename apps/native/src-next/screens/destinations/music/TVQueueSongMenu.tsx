import React, {useLayoutEffect, useRef, useState} from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Menu} from '../../../kit/overlays';
import type {NowPlayingQueueProps, NowPlayingQueueRow} from './NowPlayingQueue';

type Context = Readonly<{scopeKey: string; fenceKey: string; lease?: object}>;
type Input = Readonly<{enabled: boolean; queueId: string; revision: string; context?: Context;
  upcomingIds: ReadonlySet<string>; entry: NonNullable<NowPlayingQueueProps['queueEdit']>['entry'];
  onPlay?: NowPlayingQueueProps['onPlay']; edit?: NowPlayingQueueProps['queueEdit']; testID: string}>;
type Opening = Readonly<{row: NowPlayingQueueRow; queueId: string; revision: string; context: Context;
  play?: Input['onPlay']; edit?: Input['edit']; position?: number}>;

/** Only presentation admission. The captured command still crosses its original owner guard. */
export function useTVQueueSongMenu(input: Input) {
  const [opening, setOpening] = useState<Opening>(), own = useRef<Opening | undefined>(undefined), alive = useRef(true), latest = useRef(input);
  latest.current = input;
  const current = (at: Opening) => {
    const now = latest.current, context = now.context, facts = now.entry(at.row.entryId), editFacts = now.edit?.entry(at.row.entryId);
    return alive.current && own.current === at && now.enabled && !!context
      && context.scopeKey === at.context.scopeKey && context.fenceKey === at.context.fenceKey && context.lease === at.context.lease
      && now.queueId === at.queueId && now.revision === at.revision && now.upcomingIds.has(at.row.entryId) && facts?.available === true
      && (!at.play || !!now.onPlay)
      && (!at.edit || now.edit?.enabled === true && now.edit.fenceKey === context.fenceKey && now.edit.lease === context.lease
        && typeof at.position === 'number' && facts.position === at.position && editFacts?.available === true && editFacts.position === at.position);
  };
  const retire = (at: Opening) => {
    if (own.current !== at) return;
    own.current = undefined;
    if (alive.current) setOpening(value => value === at ? undefined : value);
  };
  useLayoutEffect(() => {alive.current = true; return () => {alive.current = false; own.current = undefined;};}, []);
  useLayoutEffect(() => {const at = own.current; if (at && !current(at)) retire(at);});
  const admitted = (row: NowPlayingQueueRow) => {
    const now = latest.current, context = now.context, facts = now.entry(row.entryId);
    if (!now.enabled || !context || !context.scopeKey || !context.fenceKey || row.current
      || !now.upcomingIds.has(row.entryId) || facts?.available !== true) return;
    const editFacts = now.edit?.entry(row.entryId);
    const edit = now.edit?.enabled && now.edit.fenceKey === context.fenceKey && now.edit.lease === context.lease
      && editFacts?.available === true && facts.position !== undefined && editFacts.position === facts.position ? now.edit : undefined;
    if (!now.onPlay && !edit) return;
    return {row, queueId: now.queueId, revision: now.revision, context, play: now.onPlay, edit, ...(edit ? {position: facts.position} : {})};
  };
  const shown = opening && current(opening) ? opening : undefined;
  return {
    canOpen: (row: NowPlayingQueueRow) => !!admitted(row),
    open: (row: NowPlayingQueueRow) => {if (!alive.current) return; const at = admitted(row); if (!at) return; own.current = at; setOpening(at);},
    // Keep the shared Menu mounted through logical close: its actual paint exit owns return.
    menu: <Menu testID={`${input.testID}-song-menu`} open={!!shown} title={shown?.row.title} presentation="sideSheet"
      onOpenChange={open => {if (!open && shown) retire(shown);}} items={shown ? [
        {id: 'play-now', label: defaultI18n.t('player.opt.playNow'), disabled: !shown.play,
          onSelect: () => {if (current(shown)) shown.play?.(shown.queueId, shown.row.entryId, shown.revision);}},
        {id: 'remove', label: defaultI18n.t('web.together.removeFromQueue'), destructive: true, disabled: !shown.edit,
          onSelect: () => {if (current(shown)) shown.edit?.apply({action: 'remove', entryId: shown.row.entryId}, shown.queueId, shown.revision);}},
      ] : []}/>,
  };
}

import {useLayoutEffect, useRef, useState} from 'react';
import type {SessionControls} from '@core/playback-controls.ts';
import type {DashboardSession, DashboardBindings} from './bindings';
import {MoveToDialog} from './MoveToDialog';

type Owner = {live: boolean; binding: DashboardBindings; key: string; id: string; read?: Read; opening?: Opening};
type Read = {owner: Owner; life: AbortController; value?: SessionControls; transferred?: boolean};
let openingSequence = 0;
type Opening = {id: number; owner: Owner; life: AbortController; controls: SessionControls; claimed: boolean;
  title: string; viewer: string; device: string; refresh: () => void};
const retire = (owner: Owner) => {owner.live = false; owner.read?.life.abort(); owner.opening?.life.abort(); owner.read = undefined; owner.opening = undefined;};

/** The menu reads actual permission/revision; a transferred timeline opening owns
 * its own cancellation through every canonical reread/retry/write. */
export function useSessionMove(binding: DashboardBindings, session: DashboardSession, title: string | undefined, menu: boolean, retained: boolean, refresh: () => void) {
  const key = JSON.stringify([session.id, session.itemId, session.kind, session.durationMs, session.live === true]);
  const current = useRef<Owner | undefined>(undefined);
  const [read, setRead] = useState<Read>(), [view, setView] = useState<{opening: Opening; busy: boolean}>();
  const owns = (owner: Owner) => current.current === owner && owner.live && owner.binding === binding && owner.key === key && !retained && binding.isCurrent();
  const reading = (read: Read) => owns(read.owner) && read.owner.read === read && !read.life.signal.aborted;
  const opening = (value: Opening) => owns(value.owner) && value.owner.opening === value && !value.life.signal.aborted;
  // Publish only at commit, then sample once for this setup's original lifetime.
  const setupPorts = useRef({session});
  useLayoutEffect(() => {setupPorts.current = {session};});
  useLayoutEffect(() => {
    const {session} = setupPorts.current;
    const owner: Owner = {live: true, binding, key, id: session.id}; current.current = owner;
    return () => {retire(owner); if (current.current === owner) current.current = undefined;};
  }, [binding, key, retained]);
  // Publish only at commit, then sample once for this setup's original lifetime.
  const setupPorts1 = useRef({reading, owns});
  useLayoutEffect(() => {setupPorts1.current = {reading, owns};});
  useLayoutEffect(() => {
    const {reading, owns} = setupPorts1.current;
    const owner = current.current, load = binding.read.controls;
    if (!menu || !owner || !owns(owner) || !load || !binding.effects.seek) return;
    const read: Read = {owner, life: new AbortController()}; owner.read = read;
    void (async () => {
      try {
        const value = await load(owner.id, read.life.signal);
        if (!reading(read)) return;
        if (value.sessionId !== owner.id) throw new Error('dashboard_controls_session_mismatch');
        read.value = value; setRead(read);
      } catch (error) {if (reading(read)) binding.effects.reportError(error);}
    })();
    return () => {read.life.abort(); if (owner.read === read) owner.read = undefined;};
  }, [binding, key, retained, menu]);
  const value = read && reading(read) ? read.value : undefined;
  const canMove = !!value && !read?.transferred && !!binding.effects.seek && value.actions.includes('seek') && typeof value.durationMs === 'number'
    && Number.isSafeInteger(value.durationMs) && value.durationMs > 0 && Number.isSafeInteger(value.positionMs) && value.positionMs >= 0 && value.positionMs <= value.durationMs && !!title;
  const close = (value: Opening) => {
    value.life.abort();
    if (value.owner.opening === value) value.owner.opening = undefined;
    setView(current => current?.opening === value ? undefined : current);
  };
  const open = () => {
    if (!canMove || !read || read.transferred || !value || !reading(read)) return;
    read.transferred = true;
    const owner = read.owner; owner.opening?.life.abort();
    const original: Opening = {id: ++openingSequence, owner, life: new AbortController(), controls: value, claimed: false,
      title: title!, viewer: session.user, device: session.device, refresh};
    owner.opening = original; setView({opening: original, busy: false});
  };
  const original = view?.opening, shown = original && opening(original);
  const move = async (value: Opening, positionMs: number) => {
    const seek = value.owner.binding.effects.seek;
    if (!opening(value) || value.claimed || !seek || !Number.isSafeInteger(positionMs) || positionMs < 0 || positionMs > value.controls.durationMs!) return;
    value.claimed = true; setView({opening: value, busy: true});
    try {
      await seek(value.controls.sessionId, positionMs, value.life.signal);
      if (opening(value)) {close(value); if (owns(value.owner)) value.refresh();}
    } catch (error) {
      if (opening(value)) {close(value); if (owns(value.owner)) value.owner.binding.effects.reportError(error);}
      // A new explicit menu opening obtains a fresh real controls receipt before
      // any retry; no failed/uncertain command is silently resubmitted.
    }
  };
  return {canMove, open, dialog: shown && <MoveToDialog key={original.id} title={original.title} viewer={original.viewer} device={original.device}
    durationMs={original.controls.durationMs!} positionMs={original.controls.positionMs} busy={view!.busy}
    onCancel={() => {if (opening(original) && !original.claimed) close(original);}}
    onMove={positionMs => {void move(original, positionMs);}} />};
}

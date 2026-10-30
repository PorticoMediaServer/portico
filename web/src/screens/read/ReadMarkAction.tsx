import {useCallback, useLayoutEffect, useRef, useState} from 'react';
import {ReadingError, type ReadingClient, type ReadingReadMark, type ReadingReadMarkCommand} from '@portico/client-core';
import {defaultI18n} from '@i18n';
import {Button} from '../../kit/controls';

type Opening = {client: ReadingClient; itemId: string; controller: AbortController; live: boolean; busy: boolean; command?: ReadingReadMarkCommand; needsRead: boolean};
type View = Readonly<{opening?: Opening; mark?: ReadingReadMark; busy?: boolean; failed?: boolean; unavailable?: boolean}>;
const definite = new Set(['personal_state_conflict', 'personal_needs_resolution', 'personal_needs_review', 'operation_expired', 'idempotency_key_reused', 'invalid_request', 'not_found', 'not_permitted', 'unauthorized']);

/** An explicit profile mark. Saved location and publication progress stay with
 * the existing Reader, and uncertain writes keep their original body/op ID. */
export function ReadMarkAction({client, itemId, isCurrent, reportError}: {client: ReadingClient; itemId: string; isCurrent: () => boolean; reportError: (error: unknown) => void}) {
  const inputs = useRef({client, itemId, isCurrent, reportError}); inputs.current = {client, itemId, isCurrent, reportError};
  const opening = useRef<Opening | undefined>(undefined), [view, setView] = useState<View>({});
  // Publish synchronously as well as through React: an old event closure must
  // retire before a successor view is committed, not at passive cleanup.
  const currentView = useRef(view);
  const publish = useCallback((next: View) => {currentView.current = next; setView(next);}, []);
  const current = useCallback((held: Opening) => held.live && opening.current === held && !held.controller.signal.aborted && !held.client.signal.aborted
    && inputs.current.client === held.client && inputs.current.itemId === held.itemId && inputs.current.isCurrent(), []);
  const load = useCallback(async (held: Opening) => {
    if (!current(held) || held.busy) return;
    held.busy = true; publish({...currentView.current, opening: held, busy: true});
    try {
      const mark = await held.client.readMark(held.itemId, held.controller.signal);
      if (!current(held)) return;
      held.needsRead = false; publish({opening: held, mark});
    } catch (error) {if (current(held)) {
      if (error instanceof ReadingError && (error.status === 403 || error.status === 404)) publish({opening: held, unavailable: true});
      else publish({...currentView.current, opening: held, busy: false, failed: true});
      inputs.current.reportError(error);
    }}
    finally {held.busy = false;}
  }, [current, publish]);
  useLayoutEffect(() => {
    const held: Opening = {client, itemId, controller: new AbortController(), live: true, busy: false, needsRead: true};
    opening.current = held; publish({opening: held});
    if (client.canMarkRead(itemId)) void load(held);
    return () => {held.live = false; held.controller.abort(); if (opening.current === held) opening.current = undefined;};
  }, [client, itemId, load, publish]);
  const held = view.opening, mark = view.mark;
  if (!held || !current(held) || !client.canMarkRead(itemId) || view.unavailable || !mark && !view.failed) return null;
  const renderedView = view, renderedCommand = held.command, renderedNeedsRead = held.needsRead;
  const press = async () => {
    if (!current(held) || held.busy || currentView.current !== renderedView || renderedView.opening !== held || renderedView.mark !== mark
      || held.command !== renderedCommand || held.needsRead !== renderedNeedsRead) return;
    if (renderedNeedsRead || !mark) {await load(held); return;}
    const command = renderedCommand ?? Object.freeze({operationId: crypto.randomUUID(), expectedRevision: mark.revision, read: !mark.read});
    held.command = command; held.busy = true; publish({opening: held, mark, busy: true});
    let committed = false;
    try {
      const updated = await held.client.setReadMark(held.itemId, command, held.controller.signal);
      if (!current(held) || held.command !== command) return;
      if (updated.viewerFence !== mark.viewerFence || updated.revision < mark.revision) throw new ReadingError('source_changed');
      // A receipt is the original operation's result, not necessarily the
      // latest state after another device's later choice. Read current state
      // separately; failure here retries only the read, never a new write.
      held.command = undefined; held.needsRead = true; committed = true;
      const fresh = await held.client.readMark(held.itemId, held.controller.signal);
      if (!current(held)) return;
      if (fresh.viewerFence !== mark.viewerFence || fresh.revision < updated.revision) throw new ReadingError('source_changed');
      held.needsRead = false; publish({opening: held, mark: fresh});
    } catch (error) {
      if (current(held) && (held.command === command || committed)) {
        if (error instanceof ReadingError && definite.has(error.code)) {held.command = undefined; held.needsRead = true;}
        publish({opening: held, mark, failed: true}); inputs.current.reportError(error);
      }
    } finally {held.busy = false;}
  };
  const label = defaultI18n.t(view.failed ? 'next.states.tryAgain' : mark?.read ? 'next.reader.markUnread' : 'next.reader.markRead');
  return <Button variant="glass" icon="i-check" iconOnly aria-label={label} title={label} aria-pressed={mark?.read} disabled={view.busy} onClick={() => {void press();}}/>;
}

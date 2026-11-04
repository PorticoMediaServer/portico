import {useEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactNode, type RefObject} from 'react';
import {defaultI18n} from '@i18n';
import {randomId} from '@core/random-id.ts';
import type {PinnedResource, PinsApi} from '@core/library-pins.ts';
import {Button} from '../../kit/controls';
import {Dialog} from '../../kit/overlays/Dialog';
import {PinsController} from './pins-controller';
import styles from './pins.module.css';

const t = defaultI18n.t;
type Library = Readonly<{id: string; name: string}>;
type Row = Readonly<{id: string; name: string; pinned: boolean; moveable: boolean; toggle: () => void; move: (by: -1 | 1) => void}>;

/** Edit pins (phone-app 4c "Edit pins", 4d's Libraries title): pinned libraries first in the order set here, then the
 * pinned collections, saved views and playlists. The native pin editor's rows, with Move up / Move down for order. */
export function PinEditor({open, onClose, api, serverId, libraries, isCurrent, returnFocusRef}: Readonly<{open: boolean; onClose: () => void; api: PinsApi; serverId: string;
  libraries: readonly Library[]; isCurrent: () => boolean; returnFocusRef?: RefObject<HTMLElement | null>}>) {
  const [controller, setController] = useState<PinsController>();
  useEffect(() => {
    if (!open) return;
    const created = new PinsController({api, serverId, current: isCurrent, libraryIds: () => libraries.map(library => library.id), requestId: async () => randomId()});
    setController(created); void created.load();
    return () => {created.dispose(); setController(current => current === created ? undefined : current);};
  }, [open, api, serverId, isCurrent, libraries]);
  return <Dialog open={open} onOpenChange={value => {if (!value) onClose();}} title={t('pins.editTitle')} description={t('pins.librariesHint')} returnFocusRef={returnFocusRef}>
    {controller && <PinRows controller={controller} libraries={libraries}/>}
  </Dialog>;
}

function PinRows({controller, libraries}: Readonly<{controller: PinsController; libraries: readonly Library[]}>) {
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot);
  const pinned = useMemo(() => state.libraries?.pinnedLibraryIds ?? [], [state.libraries]);
  const byId = new Map(libraries.map(library => [library.id, library]));
  const ordered = state.libraries ? [...pinned.flatMap(id => byId.get(id) ?? []), ...libraries.filter(library => !pinned.includes(library.id))] : [];
  const libraryRows: Row[] = ordered.map(library => ({id: library.id, name: library.name, pinned: pinned.includes(library.id), moveable: pinned.includes(library.id),
    toggle: () => {void controller.pinLibrary(library.id, !pinned.includes(library.id));}, move: by => {void controller.moveLibrary(library.id, by);}}));
  const resources: readonly PinnedResource[] = state.resources?.order ?? [];
  const resourceRows: Row[] = resources.map(entry => ({id: `${entry.kind}:${entry.id}`, name: entry.name, pinned: true, moveable: true,
    toggle: () => {void controller.unpinResource(entry);}, move: by => {void controller.moveResource(entry, by);}}));
  return <div className={styles.editor} aria-busy={state.busy}>
    {state.phase === 'loading' && !state.libraries ? <p role="status">{t('next.settings.loading')}</p> : null}
    {state.phase === 'error' ? <div role="alert"><p>{t('next.states.pageFailedBody')}</p><Button size="small" onClick={() => void controller.load()}>{t('action.tryAgain')}</Button></div> : null}
    {state.libraries && <><h3>{t('pins.libraries')}</h3><Rows rows={libraryRows} busy={state.busy}/></>}
    {state.resources && <><h3>{t('pins.saved')}</h3>{resourceRows.length ? <Rows rows={resourceRows} busy={state.busy}/> : <p className={styles.none}>{t('pins.noneSaved')}</p>}</>}
  </div>;
}

function Rows({rows, busy}: Readonly<{rows: readonly Row[]; busy: boolean}>) {
  const moveable = rows.filter(row => row.moveable);
  return <ul className={styles.rows}>{rows.map(row => {
    const at = moveable.indexOf(row);
    return <li key={row.id}><span className={styles.name}>{row.name}</span>
      {row.moveable && <span className={styles.order}>
        <Button variant="icon" size="small" icon="i-up" iconOnly disabled={busy || at <= 0} aria-label={t('pins.moveUp', {name: row.name})} onClick={() => row.move(-1)}/>
        <Button variant="icon" size="small" icon="i-down" iconOnly disabled={busy || at < 0 || at >= moveable.length - 1} aria-label={t('pins.moveDown', {name: row.name})} onClick={() => row.move(1)}/>
      </span>}
      <Button size="small" variant={row.pinned ? 'glass' : 'primary'} disabled={busy} aria-label={t(row.pinned ? 'pins.unpinNamed' : 'pins.pinNamed', {name: row.name})} onClick={row.toggle}>{t(row.pinned ? 'pins.unpin' : 'pins.pin')}</Button>
    </li>;
  })}</ul>;
}

/** A library screen's Edit pins (phone-app 4c ⋯): open() from the menu, and the editor element to mount. */
export function usePinEditor(context: Readonly<{api: PinsApi; isCurrent: () => boolean; scope: {serverId: string}; identity?: {libraries: readonly Library[]}}>): Readonly<{open: () => void; element: ReactNode}> {
  const [open, setOpen] = useState(false), trigger = useRef<HTMLElement | null>(null);
  const libraries = context.identity?.libraries ?? noLibraries;
  return {open: () => {if (!context.isCurrent()) return; trigger.current = document.activeElement instanceof HTMLElement ? document.activeElement : null; setOpen(true);},
    element: <PinEditor open={open} onClose={() => setOpen(false)} api={context.api} serverId={context.scope.serverId} libraries={libraries} isCurrent={context.isCurrent} returnFocusRef={trigger}/>};
}
const noLibraries: readonly Library[] = Object.freeze([]);

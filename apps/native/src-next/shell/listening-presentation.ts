import {sameFence, type DockState, type PlaybackDock} from '../../../../packages/player-core/src';
import type {DockOccupant, DockPresentation} from '../kit/dock/types';

/** Expanded local listening is a page, not a dock occupant. The real dock's
 * occupant() deliberately omits expanded locals; project only the already
 * admitted current snapshot, without creating a new admission or lifetime. */
export function expandedListeningOccupant(dock: PlaybackDock, snapshot: DockState, presentation: DockPresentation | undefined): DockOccupant | null {
  const occupant = localListeningOccupant(dock, snapshot, presentation);
  return occupant?.playback.expanded ? occupant : null;
}

/** Read-only paint of the same already-admitted local. This creates no expanded
 * destination, command authority or reader lifetime for a miniature preview. */
export function localListeningOccupant(dock: PlaybackDock, snapshot: DockState, presentation: DockPresentation | undefined): DockOccupant | null {
  const local = snapshot.local;
  if (dock.getSnapshot() !== snapshot || snapshot.remote || !local || !presentation
    || local.kind !== 'music' && local.kind !== 'audiobook' || !sameFence(local.fence, presentation.fence)) return null;
  return {mode: local.kind, playback: local, presentation};
}


export type ListeningPaintOpening<T> = Readonly<{open: boolean; key: object; scope: T}>;
/** A pure UI opening keeps its original page while full. Mini paint may capture
 * a new current page, but a still-open full page never adopts that successor. */
export function captureListeningOpening<T>(previous: ListeningPaintOpening<T> | undefined, open: boolean, scope: T): ListeningPaintOpening<T> {
  if (open && previous?.open) return previous;
  if (!open && previous && !previous.open && previous.scope === scope) return previous;
  return Object.freeze({open, key: open || !previous ? Object.freeze({}) : previous.key, scope});
}

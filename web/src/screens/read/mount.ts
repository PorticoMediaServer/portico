import {webReaderMotion} from './motion-policy';
import {readerErrorCode, type ReaderController, type ReaderCrease, type ReaderErrorCode, type ReaderFormat, type ReaderLocation, type ReaderMetadata, type ReaderSource, type ReaderViewport} from '@portico/reader-core';

export type ReaderMountAuthority = Readonly<{
  isCurrent: () => boolean;
  /** The existing viewer/origin lifetime supplies its real retirement event. */
  subscribe: (listener: () => void) => (() => void);
}>;
/** Public session methods only: the canonical Reading facade deliberately
 * omits the low-level preference updater and the controller's private fields. */
export type ReaderHostController<P extends object> = Omit<ReaderController<P>, 'updatePreferences'>;
export type ReaderSurfaceMountOptions<P extends object> = Readonly<{
  controller: ReaderHostController<P>; source: ReaderSource; format: ReaderFormat; initialLocation?: ReaderLocation;
  /** Actual manifest identity; an omitted author stays omitted in the engine and chrome. */
  metadata?: ReaderMetadata;
  /** Actual authorized publication session lifetime, separate from viewer scope. */
  signal?: AbortSignal;
  authority: ReaderMountAuthority; container: HTMLElement;
  reportError: (code: ReaderErrorCode) => void;
  /** Await disposal before reusing a DOM container (including StrictMode reconnect). */
  settled?: Promise<void>;
  /** Actual platform book fold for this window; absent when no hinge is reported. */
  crease?: ReaderCrease;
  /** Page arrows' reach from each side edge (px), while they are drawn. */
  turnClearance?: number;
}>;
const key = (crease: ReaderCrease | undefined, clearance?: number) => (crease ? `${crease.left}:${crease.right}` : '') + (clearance ? `|${clearance}` : '');
export type ReaderSurfaceMount = Readonly<{ready: Promise<void>; close: () => Promise<void>; setCrease: (crease: ReaderCrease | undefined) => void; setTurnClearance: (clearance: number | undefined) => void}>;

/** The core owns the document, preferences, location and view; this owns only DOM lifetime. */
export function mountReaderSurface<P extends object>({controller, source, format, initialLocation, metadata, signal, authority, container, reportError, settled = Promise.resolve(), crease: initialCrease, turnClearance: initialClearance}: ReaderSurfaceMountOptions<P>): ReaderSurfaceMount {
  let active = true, opened = false, opening = false, frame: number | undefined, width = 0, height = 0, crease = initialCrease, turnClearance = initialClearance;
  // Applied engine state, not desired: a same-size posture change must still reach
  // the engine. In-flight resizes serialize and then drain the latest request.
  let engineKey = '', inflight = false, dirty = false;
  let closeResult: Promise<void> | undefined;
  const current = () => active && authority.isCurrent() && !signal?.aborted;
  const viewport = (): ReaderViewport => ({width, height, ...(crease ? {crease} : {}), ...(turnClearance ? {turnClearance} : {})});
  let offAuthority = () => {}, offSession = () => {}, resolveSize: (() => void) | undefined;
  const waitForSize = new Promise<void>(resolve => { resolveSize = resolve; });
  const observer = new ResizeObserver(() => {
    if (container.clientWidth > 0 && container.clientHeight > 0) resolveSize?.();
    measure();
  });
  const close = (): Promise<void> => {
    if (closeResult) return closeResult;
    active = false; observer.disconnect(); offAuthority(); offSession(); resolveSize?.();
    signal?.removeEventListener('abort', aborted);
    if (frame !== undefined) cancelAnimationFrame(frame);
    // A superseded effect that never opened must not close a newer core session.
    const closing = opened || opening ? controller.close() : Promise.resolve();
    // An abandoned reconnect must retain its predecessor's disposal fence.
    closeResult = Promise.all([settled, closing]).then(() => {});
    return closeResult;
  };
  const aborted = () => {void close().catch(() => {});};
  const failed = (error: unknown) => { if (current()) reportError(readerErrorCode(error)); };
  const resize = () => {
    frame = undefined;
    if (!current() || controller.getSnapshot().status !== 'ready') return;
    const nextWidth = container.clientWidth, nextHeight = container.clientHeight, nextCrease = key(crease, turnClearance);
    if (nextWidth <= 0 || nextHeight <= 0) return;
    if (inflight) {dirty = true; return;}
    if (nextWidth === width && nextHeight === height && nextCrease === engineKey) return;
    width = nextWidth; height = nextHeight; inflight = true; dirty = false;
    const requestedKey = nextCrease;
    void controller.resize(viewport()).then(() => {
      // The controller committed this exact size and crease.
      engineKey = requestedKey;
    }, error => {
      // A refused resize is not an applied viewport. Let a later observation of
      // the same size retry, without invalidating a newer resize's dimensions.
      if (current() && width === nextWidth && height === nextHeight) {width = 0; height = 0;}
      failed(error);
    }).finally(() => {
      inflight = false;
      // A->B->A while B was in flight must still issue A after B commits.
      if (current() && dirty) {dirty = false; resize();}
    });
  };
  const measure = () => {
    if (current() && opened && frame === undefined) frame = requestAnimationFrame(resize);
  };
  if (current()) {
    signal?.addEventListener('abort', aborted, {once: true});
    offAuthority = authority.subscribe(() => { if (!authority.isCurrent()) void close().catch(() => {}); });
  }
  if (!active) offAuthority();
  const ready = (async () => {
    await settled;
    if (!current()) return;
    observer.observe(container);
    if (container.clientWidth <= 0 || container.clientHeight <= 0) {
      await waitForSize;
    }
    if (!current()) return;
    width = container.clientWidth; height = container.clientHeight;
    opening = true;
    const openedCrease = key(crease, turnClearance);
    await controller.open({source, format, container, metadata, signal, motion: webReaderMotion(container.ownerDocument), location: initialLocation, viewport: viewport()});
    opening = false;
    if (current()) {engineKey = openedCrease;}
    if (!current()) { await close(); return; }
    opened = true;
    // A replacement open retires this mount before its old parent can abort.
    // The controller already owns that disposal; never close the new session.
    offSession = controller.subscribe(() => {
      const snapshot = controller.getSnapshot();
      if (snapshot.status !== 'ready' || snapshot.sourceId !== source.id || snapshot.sourceRevision !== source.revision) {
        opened = false; void close().catch(() => {});
      }
    });
    measure();
  })().catch(failed);
  const setCrease = (next: ReaderCrease | undefined) => {
    crease = next;
    // A refused same-size posture change is retried by the next observation; a
    // zero-size or not-yet-open surface takes the crease when it opens.
    if (opened) {frame = undefined; resize();}
  };
  const setTurnClearance = (next: number | undefined) => {
    turnClearance = next;
    if (opened) {frame = undefined; resize();}
  };
  return Object.freeze({ready, close, setCrease, setTurnClearance});
}

import type {ReactNode} from 'react';
export type WindowChromeSurface = 'pages' | 'video' | 'music';
export type WindowChromeState = Readonly<{
  surface: WindowChromeSurface;
  controlsVisible: boolean;
  fullscreen: boolean;
  scrolled?: boolean;
}>;
export type WindowChromeEnvironment = Readonly<{
  os: 'macos' | 'windows' | 'linux';
  /** The native window's state, independent of the DOM fullscreen API. */
  fullscreen: boolean;
}>;
export type WindowChromeHost = Readonly<{
  /** Optional native caption presenter; ordinary web supplies none. */
  render?: ReactNode;
  update: (state: WindowChromeState) => void;
  dispose?: () => void;
}>;
export type WindowChromeInstallation = Readonly<{
  updateEnvironment: (environment: WindowChromeEnvironment) => void;
  dispose: () => void;
}>;
export type WindowChromePublisher = Readonly<{
  update: (state: Omit<WindowChromeState, 'surface'>) => void;
  dispose: () => void;
}>;

type Publication = {order: number; surface: WindowChromeSurface; state?: WindowChromeState};
type Installation = {host: WindowChromeHost; environment?: WindowChromeEnvironment; last?: WindowChromeState; disposed: boolean};
const pages: WindowChromeState = Object.freeze({surface: 'pages', controlsVisible: true, fullscreen: false, scrolled: false});
const publications = new Set<Publication>(), environmentListeners = new Set<() => void>();
let active: Installation | undefined, order = 0;

export function getWindowChromeRenderer(): ReactNode | undefined { return active?.host.render; }
export function getWindowChromeEnvironment(): WindowChromeEnvironment | undefined { return active?.environment; }
export function subscribeWindowChromeEnvironment(listener: () => void): () => void {
  environmentListeners.add(listener);
  return () => { environmentListeners.delete(listener); };
}
function environmentChanged() { for (const listener of environmentListeners) listener(); }
function sameState(a: WindowChromeState | undefined, b: WindowChromeState) {
  return a !== undefined && a.surface === b.surface && a.controlsVisible === b.controlsVisible && a.fullscreen === b.fullscreen && a.scrolled === b.scrolled;
}
function publish() {
  const installation = active;
  if (!installation) return;
  let current: Publication | undefined;
  for (const candidate of publications) {
    if (!candidate.state) continue;
    const candidateMedia = candidate.surface !== 'pages', currentMedia = current !== undefined && current.surface !== 'pages';
    if (!current || (candidateMedia && !currentMedia) || (candidateMedia === currentMedia && candidate.order > current.order)) current = candidate;
  }
  const state = current?.state ?? pages;
  const next = Object.freeze({...state, fullscreen: state.fullscreen || installation.environment?.fullscreen === true});
  if (sameState(installation.last, next)) return;
  installation.last = next;
  installation.host.update(next);
}
function retire(installation: Installation) {
  if (installation.disposed) return;
  installation.disposed = true;
  if (active === installation) { active = undefined; environmentChanged(); }
  installation.host.dispose?.();
}

/** Desktop supplies observed native facts through the returned handle. Web installs no host. */
export function installWindowChromeHost(host: WindowChromeHost): WindowChromeInstallation {
  if (active) retire(active);
  const installation: Installation = {host, disposed: false};
  active = installation;
  environmentChanged();
  publish();
  return Object.freeze({
    updateEnvironment(environment: WindowChromeEnvironment) {
      if (active !== installation || installation.disposed) return;
      const previous = installation.environment;
      if (previous?.os === environment.os && previous.fullscreen === environment.fullscreen) return;
      installation.environment = Object.freeze({...environment});
      environmentChanged();
      publish();
    },
    dispose() { retire(installation); },
  });
}

/** Mounted media controls override pages until disposed; updates after disposal are ignored. */
export function createWindowChromePublisher(surface: WindowChromeSurface): WindowChromePublisher {
  const publication: Publication = {surface, order: ++order};
  let disposed = false;
  publications.add(publication);
  return Object.freeze({
    update(state: Omit<WindowChromeState, 'surface'>) {
      if (disposed) return;
      publication.state = Object.freeze({...state, surface});
      publish();
    },
    dispose() {
      if (disposed) return;
      disposed = true;
      publications.delete(publication);
      publish();
    },
  });
}

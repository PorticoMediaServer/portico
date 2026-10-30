import {useLayoutEffect, useRef, useSyncExternalStore} from 'react';
import type {ChromeUpdate, WindowAction, WindowChromeHostState, WindowSnapshot} from './window';
import styles from './caption-controls.module.css';
type Snapshot = Readonly<{native: WindowSnapshot; state: WindowChromeHostState}>;
type Ports = Readonly<{action: (action: WindowAction) => Promise<void>; geometry: (rects: ChromeUpdate['captions']) => void}>;
/** Native snapshot supplies translated OS labels and state. These mounted
 * controls supply real rectangles; native validation keeps the final authority. */
export function createCaptionControls(ports: Ports) {
  let value: Snapshot | undefined, key = '', disposed = false;
  const listeners = new Set<() => void>();
  const source = {
    snapshot: () => value,
    subscribe: (listener: () => void) => {listeners.add(listener);return () => {listeners.delete(listener);};},
    action: (action: WindowAction) => {if (!disposed) void ports.action(action);},
    geometry: (rects: ChromeUpdate['captions']) => {if (!disposed) ports.geometry(rects);},
  };
  return {
    render: <CaptionControls source={source}/>,
    update(native: WindowSnapshot, state: WindowChromeHostState) {
      if (disposed) return;
      const next = JSON.stringify([native, state]);if (next === key) return;
      key = next;value = {native, state};for (const listener of listeners) listener();
    },
    dispose() {if (disposed) return;disposed = true;ports.geometry(null);value = undefined;for (const listener of listeners) listener();listeners.clear();},
  };
}
type Source = {snapshot: () => Snapshot | undefined; subscribe: (listener: () => void) => () => void; action: (action: WindowAction) => void; geometry: (rects: ChromeUpdate['captions']) => void};
function CaptionControls({source}: {source: Source}) {
  const snapshot = useSyncExternalStore(source.subscribe, source.snapshot, () => undefined);
  const group = useRef<HTMLDivElement>(null);
  const visible = snapshot?.native.os === 'windows' && !snapshot.native.fullscreen && !snapshot.state.fullscreen && (snapshot.state.surface === 'pages' || snapshot.state.controlsVisible) && !!snapshot.native.captions;
  useLayoutEffect(() => {
    if (!visible || !group.current) {source.geometry(null);return;}
    const element = group.current;let last = '';
    const measure = () => {
      const buttons = element.querySelectorAll('button');if (buttons.length !== 3) return;
      const rects = [...buttons].map(button => {const r = button.getBoundingClientRect();return {x: r.x, y: r.y, width: r.width, height: r.height};});
      const next = {minimize: rects[0]!, maximize: rects[1]!, close: rects[2]!}, key = JSON.stringify(next);
      if (key !== last) {last = key;source.geometry(next);}
    };
    const observer = new ResizeObserver(measure);observer.observe(element);window.addEventListener('resize', measure);measure();
    return () => {observer.disconnect();window.removeEventListener('resize', measure);source.geometry(null);};
  }, [source, visible]);
  if (!visible || !snapshot?.native.captions) return null;
  const {native} = snapshot, names = native.captions!;
  return <div ref={group} className={styles.captions} data-desktop-window-chrome data-window-no-drag data-focused={native.focused}>
    <button type="button" aria-label={names.minimize} title={names.minimize} data-hover={native.captionHover === 1} onClick={() => source.action('minimize')}><svg width="10" height="10" viewBox="0 0 10 10" aria-hidden="true"><path d="M0 5.5h10"/></svg></button>
    <button type="button" aria-label={native.maximized ? names.restore : names.maximize} title={native.maximized ? names.restore : names.maximize} data-hover={native.captionHover === 2} onClick={() => source.action('toggleMaximize')}><svg width="10" height="10" viewBox="0 0 10 10" aria-hidden="true">{native.maximized ? <><rect x=".5" y="2.5" width="7" height="7" rx="1"/><path d="M2.5 2.5V1.5a1 1 0 0 1 1-1h5a1 1 0 0 1 1 1v5a1 1 0 0 1-1 1h-1"/></> : <rect x=".5" y=".5" width="9" height="9" rx="1"/>}</svg></button>
    <button type="button" className={styles.close} aria-label={names.close} title={names.close} data-hover={native.captionHover === 3} onClick={() => source.action('close')}><svg width="10" height="10" viewBox="0 0 10 10" aria-hidden="true"><path d="M.5.5l9 9M9.5.5l-9 9"/></svg></button>
  </div>;
}

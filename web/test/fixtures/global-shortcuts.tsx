import {createRoot} from 'react-dom/client';
import {GlobalShortcuts} from '../../src/shell/GlobalShortcuts';

/** Isolated keyboard harness; the component and dialog are the production ones. */
export function mountGlobalShortcuts() {
  const host = document.createElement('div');
  document.body.append(host);
  const root = createRoot(host);
  root.render(<><button data-shortcut-origin>Origin</button><input aria-label="Editable"/>
    <GlobalShortcuts playback={{skips: {back: 7, forward: 45}}}/></>);
  return () => {root.unmount(); host.remove();};
}

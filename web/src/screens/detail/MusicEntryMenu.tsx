import type {ReactNode} from 'react';
import {MenuGroup} from '../../kit/overlays';
import type {ScreenContext} from './context';
import {StartRadioMenuItem, type StartRadioSeed} from './StartRadio';

export type MusicEntryMenuProps = Readonly<{
  /** The title page's captured context; the same one its own actions use. */
  context: ScreenContext;
  /** The exact opened row: its immutable id and the title it was drawn with. */
  seed: StartRadioSeed;
  /** The caller's admission lifetime for Start radio; retirement aborts it. */
  signal: AbortSignal;
  /** The caller's original opening witness: same row, revision and page as the menu was drawn for. */
  current: () => boolean;
  /** The caller's own actually offered rows and groups, in the order they want after Start radio. */
  children?: ReactNode;
}>;

/** One music row's menu content, for any existing Menu: Start radio first, then only the rows the
 * caller really offers. Nothing is inferred from the viewer's role, and the seed id, opening
 * witness and lifetime are the caller's own — this wrapper adds no authority of its own. */
export function MusicEntryMenu({context, seed, signal, current, children}: MusicEntryMenuProps) {
  // Captured at render: a caller's mutable seed object cannot retarget the action later.
  const captured: StartRadioSeed = Object.freeze({id: seed.id, title: seed.title});
  return <MenuGroup>
    <StartRadioMenuItem context={context} seed={captured} signal={signal} current={current} />
    {children}
  </MenuGroup>;
}

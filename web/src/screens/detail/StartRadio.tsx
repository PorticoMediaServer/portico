import {t} from './text';
import {MenuItem} from '../../kit/overlays';
import {runScreenCommand, type ScreenContext} from './context';

/** The exact row a radio queue is seeded from: its stable id and the title it was drawn with. */
export type StartRadioSeed = Readonly<{id: string; title: string}>;

/** music-plus frames 7 and 8: a song's or album's menu starts with Start radio. The server builds
 * and refills the queue from the seed's sound — never a loaded page or a client ranking — and this
 * action carries the caller's original seed witness unchanged through admission. */
export function StartRadioMenuItem({context, seed, signal, current}: {
  context: ScreenContext; seed: StartRadioSeed; signal: AbortSignal; current: () => boolean;
}) {
  const playRadio = context.effects.playRadio;
  // The handler keeps the immutable seed id captured for this render, never the caller's object.
  const seedId = seed.id;
  if (!playRadio || !seedId) return null;
  return <MenuItem icon="i-radio" onSelect={() => runScreenCommand(context, () => {
    // The same current original row, seed and revision must still be the one that was drawn,
    // under the lease this menu rendered (a retired lease's signal is already aborted).
    if (signal.aborted || !current() || !context.isCurrent()) throw new DOMException('Radio seed changed', 'AbortError');
    return playRadio(seedId, signal, () => !signal.aborted && current() && context.isCurrent());
  })}>{t('music.startRadio')}</MenuItem>;
}

import type {BrowserAccountEntry} from '../../platform/account-entry';
import type {CapturedPorticoReturn} from '../../platform/portico-return-dispatch';
import type {MoveCodeLink} from '@core/account-move.ts';

type Entry = Pick<BrowserAccountEntry, 'finishPorticoReturn' | 'inspectMoveLink' | 'loadProfiles' | 'getSnapshot' | 'inspect'>;
/** Startup scrubbed this original callback. Only Entry's protected controller
 * may choose its KIND, verify it and admit its family. */
export async function initializeAccountSignIn(entry: Entry, ports: Readonly<{
  takeReturn?: () => CapturedPorticoReturn | undefined;
  takeMove?: () => MoveCodeLink | undefined;
  selectedOrigin: () => Promise<string | undefined>;
  pageOrigin: string;
  isCurrent: () => boolean;
  username: (value: string) => void;
}>) {
  if (!ports.isCurrent()) return;
  const captured = ports.takeReturn?.();
  if (captured) {await entry.finishPorticoReturn(captured); return;}
  const move = ports.takeMove?.();
  if (move) {
    if (ports.isCurrent()) await entry.inspectMoveLink(ports.pageOrigin, move);
    return;
  }
  const savedOrigin = await ports.selectedOrigin();
  if (!ports.isCurrent()) return;
  if (savedOrigin) {
    await entry.loadProfiles();
    if (!ports.isCurrent()) return;
    const state = entry.getSnapshot();
    if (state.phase === 'profiles' && state.snapshot && !state.snapshot.twoStepSetupRequired || state.phase === 'ready') return;
    if (state.snapshot) ports.username(state.snapshot.account.username);
    // A restored restricted grant has no live password. Re-enter the drawn
    // sign-in on its actual proved/saved origin; never enroll on restoration.
    await entry.inspect(state.origin ?? savedOrigin);
    return;
  }
  await entry.inspect(ports.pageOrigin);
}

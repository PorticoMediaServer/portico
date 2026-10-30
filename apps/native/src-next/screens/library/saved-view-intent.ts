import type {SavedDefinition} from '../../../../../packages/client-core/src/personal-saved';
import type {NativeShellRoute} from '../../shell/routes';
import {sameAuthority, type ShellAuthority} from '../../shell/model';

/** One exact saved definition headed for one exact Library route. The route object and
 * the captured authority/session family must match when the Library takes it. The
 * owner's token scopes cleanup so a successor intent is never cleared by an older
 * subscription. */
export type LibrarySavedViewIntent = Readonly<{
  token: object;
  release: () => void;
  route: NativeShellRoute;
  libraryId: string;
  authority: ShellAuthority;
  sessionFamilyId: string;
  definition: SavedDefinition;
  resourceId: string;
  revision: number;
  viewerFence: string;
}>;

let pending: LibrarySavedViewIntent | undefined;

export function askLibrarySavedView(intent: LibrarySavedViewIntent): void {
  const previous = pending; pending = intent; previous?.release();
}

export function takeLibrarySavedView(route: NativeShellRoute, libraryId: string, authority: ShellAuthority, sessionFamilyId: string | undefined): LibrarySavedViewIntent | undefined {
  const intent = pending;
  if (!intent) return undefined;
  if (intent.route !== route) return undefined;
  pending = undefined;
  intent.release();
  if (!sameAuthority(intent.authority, authority) || !sessionFamilyId || intent.sessionFamilyId !== sessionFamilyId || intent.libraryId !== libraryId) return undefined;
  return intent;
}

export function clearLibrarySavedViewIntent(token?: object): void {
  if (!pending || token && pending.token !== token) return;
  const intent = pending; pending = undefined; intent.release();
}

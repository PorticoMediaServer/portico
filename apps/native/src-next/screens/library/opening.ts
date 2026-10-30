import type {SavedDefinition} from '../../../../../packages/client-core/src/personal-saved';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {ShellAuthority} from '../../shell/model';
import type {NativeShellRoute} from '../../shell/routes';

/** An exact query and the original route/viewer that may apply it. Saved's
 * resource intent structurally extends this with its own id/revision fence. */
export type NativeLibraryOpening = Readonly<{route: NativeShellRoute; libraryId: string; authority: ShellAuthority;
  sessionFamilyId: string; definition: SavedDefinition}>;

export function nativeLibraryOpeningRoute(context: NativeShellBootstrapContext, route: NativeShellRoute): NativeShellRoute | undefined {
  if (!context.current() || route.kind !== 'shell' || route.destination.kind !== 'library') return undefined;
  const destination = route.destination;
  return [context.navigation.getSnapshot().route, ...context.navigation.retainedRoutes()]
    .find(candidate => candidate.kind === 'shell' && candidate.destination === destination);
}

export function nativeLibraryOpening(context: NativeShellBootstrapContext, route: NativeShellRoute, definition: SavedDefinition): NativeLibraryOpening | undefined {
  const sessionFamilyId = context.session()?.sessionFamilyId;
  if (!context.current() || !sessionFamilyId || route.kind !== 'shell' || route.destination.kind !== 'library'
    || route.destination.libraryId !== definition.libraryId) return undefined;
  const origin = nativeLibraryOpeningRoute(context, route);
  return origin ? Object.freeze({route: origin, libraryId: definition.libraryId, authority: context.authority(), sessionFamilyId, definition}) : undefined;
}

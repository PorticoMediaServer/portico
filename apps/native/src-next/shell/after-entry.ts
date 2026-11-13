import type {NativeShellRoute} from './routes';

/** Add profile or Manage profiles on Who's watching, before any profile is open: the main profile is entered
 * first (rules 25 and 27 of 01-sign-in: managing works only from it, behind its PIN), then the shell opens
 * Profiles. One request, bound to the main profile it was made for: the shell takes it only when that profile is
 * the one that opened, so it can never open for another. Choosing another profile or going back drops it. */
let pending: Readonly<{route: NativeShellRoute; profileId: string}> | undefined;

export const afterEntry = {
  request(route: NativeShellRoute, profileId: string) {pending = {route, profileId};},
  clear() {pending = undefined;},
  /** The route asked for when `profileId` is the profile it was asked for; any request is used up either way. */
  take(profileId: string): NativeShellRoute | undefined {const asked = pending; pending = undefined; return asked?.profileId === profileId ? asked.route : undefined;},
};

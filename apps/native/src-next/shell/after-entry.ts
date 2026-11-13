import type {NativeShellRoute} from './routes';

/** Add profile or Manage profiles on Who's watching, before any profile is open: the main profile is entered
 * first (rules 25 and 27 of 01-sign-in: managing works only from it, behind its PIN), then the shell opens
 * Profiles. One request; choosing another profile or going back drops it, and the shell takes it once. */
let pending: NativeShellRoute | undefined;

export const afterEntry = {
  request(route: NativeShellRoute) {pending = route;},
  clear() {pending = undefined;},
  take(): NativeShellRoute | undefined {const route = pending; pending = undefined; return route;},
};

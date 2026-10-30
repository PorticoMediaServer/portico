import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeShellRoute} from '../../shell/routes';
import type {NativeScreenLifetime} from './lifetime';

export type NativeTogetherPage = Readonly<{key: string; signal: AbortSignal; current: () => boolean; back: () => void}>;
const principal = (context: NativeShellBootstrapContext): string | undefined => {
  const session = context.session();
  return session && JSON.stringify([session.sessionFamilyId, session.tokenGeneration, session.authorizationHorizon, session.deviceId, session.installationId]);
};
/** One original admitted render. Leaving retires its callbacks permanently;
 * the persistent room and follower remain owned by the authority. */
export function nativeTogetherPages(context: NativeShellBootstrapContext, lifetime: Pick<NativeScreenLifetime, 'current' | 'signal'>) {
  type Issued = Readonly<{snapshot: ReturnType<typeof context.navigation.getSnapshot>; principal?: string; page: NativeTogetherPage; retire: () => void}>;
  let alive = true, sequence = 0, generation = 0, original: Issued | undefined;
  return {
    capture(route: Extract<NativeShellRoute, {kind: 'together'}>): NativeTogetherPage | undefined {
      if (!alive || lifetime.signal.aborted) return;
      const ticket = ++generation, previous = original;
      const ownsCapture = () => alive && !lifetime.signal.aborted && generation === ticket && original === previous;
      const snapshot = context.navigation.getSnapshot(); if (!ownsCapture()) return;
      const identity = principal(context); if (!ownsCapture()) return;
      if (previous?.snapshot === snapshot && previous.principal === identity) return previous.page;
      previous?.retire(); if (!ownsCapture()) return;
      const projection = context.navigation.presentationStack().at(-1), occurrence = projection?.occurrence;
      if (!ownsCapture()) return;
      const proof = projection && context.navigation.bindPresentation(projection, {current: lifetime.current, subscribe: context.subscribeAuthority});
      if (!ownsCapture()) {proof?.dispose(); return;}
      const abort = new AbortController();
      let retired = false, registration: Issued, offNavigation = () => {}, offAuthority = () => {};
      const registered = () => alive && !retired && !lifetime.signal.aborted && original === registration;
      const retire = () => {
        if (retired) return;
        retired = true;
        const navigation = offNavigation, authority = offAuthority; offNavigation = offAuthority = () => {};
        abort.abort(); navigation(); authority(); proof?.dispose();
      };
      const owned = () => registered() && lifetime.current() && registered() && context.current() && registered() && !!proof?.activeCurrent() && registered();
      const current = () => {
        if (!owned() || snapshot.route !== route || projection?.route !== route || !occurrence || identity === undefined
          || context.navigation.getSnapshot() !== snapshot || !owned()
          || context.navigation.presentationStack().at(-1) !== projection || !owned()
          || principal(context) !== identity || !owned()) {retire(); return false;}
        return true;
      };
      // This host key separates rendered page openings, including Back restoration.
      // Admission and pop always use the original real occurrence above.
      const page: NativeTogetherPage = Object.freeze({key: JSON.stringify([occurrence, ++sequence]), signal: abort.signal, current,
        back: () => {if (current() && occurrence) context.navigation.commitNativePop(snapshot, occurrence);}});
      if (!ownsCapture()) {retire(); return;}
      registration = Object.freeze({snapshot, principal: identity, page, retire}); original = registration;
      const installed = () => alive && original === registration && !retired;
      offNavigation = context.navigation.subscribe(() => {current();});
      if (!installed()) {const off = offNavigation; offNavigation = () => {}; off(); retire(); return;}
      offAuthority = context.subscribeAuthority(() => {current();});
      if (!installed()) {const off = offAuthority; offAuthority = () => {}; off(); retire(); return;}
      if (!current() || !installed()) {retire(); return;}
      return page;
    },
    dispose() {if (!alive) return; alive = false; generation++; const previous = original; original = undefined; previous?.retire();},
  };
}

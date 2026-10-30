import type {NativeShellNavigation, NativeShellRoute, NativeRouteSnapshot} from '../../shell/routes';

type ReadTarget = Extract<NativeShellRoute, {kind: 'read'}>;

/** One Back command for the exact admitted render. Retained or replaced readers
 * cannot spend it against the route that happens to be current at press time. */
export function readRouteBack(navigation: Pick<NativeShellNavigation, 'getSnapshot' | 'back'>,
  expected: NativeRouteSnapshot, target: ReadTarget, current: () => boolean): () => void {
  let spent = false, checking = false;
  return () => {
    if (spent || checking) return;
    checking = true;
    try {
      if (!current() || expected.route !== target || navigation.getSnapshot() !== expected) return;
      if (!current() || navigation.getSnapshot() !== expected) return;
      spent = true;
      navigation.back(expected.authority);
    } finally {checking = false;}
  };
}

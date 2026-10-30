import {nativeShellPermissionDenied} from './bootstrap-services';

type Viewer = Readonly<Record<'accountId' | 'profileId' | 'serverId' | 'authority', unknown>> & {role?: string};

/** A refused privileged read after a demotion is a role change, not a sign-out. Ask the server who
 * this viewer is now: a changed role with the same identity rebuilds; a proven 401/403 or a
 * different identity ends. A transient failure (network, timeout, 5xx) keeps the shell and lets a
 * later refusal probe again. */
export function createRoleProbe(options: {
  alive: () => boolean; viewer: () => Viewer | undefined;
  probe: () => Promise<{viewer: Viewer}> | undefined; rebuild: () => void; end: () => void;
}) {
  let running = false;
  return () => {
    if (!options.alive() || running) return;
    const current = options.viewer(), before = current?.role;
    const request = options.probe();
    running = true;
    if (!request) {options.end(); return;}
    request.then(me => {
      const now = options.viewer();
      if (options.alive() && now && before !== undefined && me.viewer.role !== before
        && (['accountId', 'profileId', 'serverId', 'authority'] as const).every(key => me.viewer[key] === now[key])) options.rebuild();
      else options.end();
    }, error => {if (nativeShellPermissionDenied(error)) options.end(); else running = false;});
  };
}

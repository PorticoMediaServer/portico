import {useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {useProtectedWebScreenContext} from '../../route-registry-view';
import type {ShellAssemblyContext} from '../../shell/bootstrap';
import {ViewerFeedback} from './feedback-controller';
import {installFeedback} from './feedback';
import {FeedbackDialog} from '../../kit/accounts/FeedbackDialog';

type FeedbackSession = Pick<ReturnType<ShellAssemblyContext['session']>, 'sessionFamilyId' | 'viewer'>;
type FeedbackLease = Readonly<{context: ShellAssemblyContext; session: FeedbackSession; controller: ViewerFeedback; key: number; current: () => boolean}>;
const sameSession = (left: FeedbackSession, right: FeedbackSession) => left.sessionFamilyId === right.sessionFamilyId
  && (['serverId', 'authority', 'accountId', 'profileId', 'role'] as const).every(key => left.viewer[key] === right.viewer[key]);

export function FeedbackHost() {
  const captured = useProtectedWebScreenContext();
  const [published, publish] = useState<FeedbackLease>(), generation = useRef(0);
  const context = captured?.context, session = context?.session(), viewer = session?.viewer;
  // Publish only at commit, then sample once for this setup's original lifetime.
  const setupPorts = useRef({session});
  useLayoutEffect(() => {setupPorts.current = {session};});
  useLayoutEffect(() => {
    const {session} = setupPorts.current;
    if (!context?.isCurrent() || !session) return;
    const original: FeedbackSession = {sessionFamilyId: session.sessionFamilyId, viewer: {...session.viewer}};
    let live = true;
    const current = () => live && context.isCurrent() && sameSession(context.session(), original);
    if (!current()) return;
    const controller = new ViewerFeedback(context.api, original.viewer, current);
    const off = installFeedback(controller);
    publish({context, session: original, controller, key: ++generation.current, current});
    controller.initialize();
    return () => {live = false;off();controller.dispose();};
    // Keep the first API/viewer ports and draft across ordinary shell, route and
    // playback publications. A replay or authority replacement owns a new lease.
  }, [context, session?.sessionFamilyId, viewer?.serverId, viewer?.authority, viewer?.accountId, viewer?.profileId, viewer?.role]);
  const owned = published && published.context === context && session && sameSession(session, published.session) && published.current() ? published : undefined;
  return owned ? <BoundFeedback key={owned.key} controller={owned.controller} /> : null;
}
function BoundFeedback({controller}: {controller: ViewerFeedback}) {
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  return state.open ? <FeedbackDialog snapshot={state} title={state.target?.item.title} hasPlayback={!!state.target?.playback} problem={controller.problem()} onChange={patch => controller.change(patch)} onSubmit={() => {void controller.submit();}} onClose={controller.close} /> : null;
}

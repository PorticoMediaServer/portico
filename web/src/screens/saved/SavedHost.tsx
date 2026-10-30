import {useRef, useLayoutEffect, useState, useSyncExternalStore} from 'react';
import {useMatch} from '@tanstack/react-router';
import type {ShellAssemblyContext} from '../../shell/bootstrap';
import {useProtectedWebScreenContext} from '../../route-registry-view';
import type {ProtectedLibraryScreen} from '../browse/screen-context';
import {createRetainedSavedOwner, type RetainedSavedOwner} from './retained-owner';

const owners = new WeakMap<ShellAssemblyContext, RetainedSavedOwner>(), listeners = new Set<() => void>();
const changed = () => {for (const listener of [...listeners]) listener();};
const subscribe = (listener: () => void) => {listeners.add(listener);return () => {listeners.delete(listener);};};
export function useRetainedSavedOwner(context: ShellAssemblyContext): RetainedSavedOwner | undefined {
  return useSyncExternalStore(subscribe, () => context.isCurrent() ? owners.get(context) : undefined, () => undefined);
}
function CapturedSavedOwner({captured}: {captured: ProtectedLibraryScreen}) {
  const context = captured.context, session = context.session(), viewer = session.viewer;
  // Publish only at commit, then sample once for this setup's original lifetime.
  const setupPorts = useRef({captured});
  useLayoutEffect(() => {setupPorts.current = {captured};});
  useLayoutEffect(() => {
    const {captured} = setupPorts.current;
    const original = context.session(), actor = original.viewer;
    if (actor.authority !== 'local' && actor.authority !== 'hosted' || !context.isCurrent()) return;
    let live = true;
    const current = () => {
      if (!live || !context.isCurrent()) return false;
      const value = context.session(), now = value.viewer;
      return value.sessionFamilyId === original.sessionFamilyId && now.serverId === actor.serverId && now.authority === actor.authority
        && now.accountId === actor.accountId && now.profileId === actor.profileId && now.role === actor.role;
    };
    const report = captured.binding.home.effects.reportError;
    const owner = createRetainedSavedOwner({api: context.api, scope: {serverId: actor.serverId, authority: actor.authority,
      accountId: actor.accountId, profileId: actor.profileId, sessionFamilyId: original.sessionFamilyId}, deviceId: original.deviceId,
      isCurrent: current, reportError: error => {if (current()) report(error);}});
    if (owners.has(context)) {live = false;owner.dispose();throw new Error('saved_owner_already_registered');}
    owners.set(context, owner);changed();owner.start();
    return () => {live = false;if (owners.get(context) === owner) owners.delete(context);owner.dispose();changed();};
    // The first API/report/session ports stay captured. Routes, preferences and
    // playback publication never replace the retained readers or native owner.
  }, [context, session.sessionFamilyId, viewer.serverId, viewer.authority, viewer.accountId, viewer.profileId, viewer.role]);
  return null;
}
/** The registry already retains this protected host across route changes.
 * Its first real Saved visit activates it; no import or unrelated route does I/O. */
export function SavedHost() {
  const captured = useProtectedWebScreenContext(), match = useMatch({from: '/saved', shouldThrow: false});
  const [visited, setVisited] = useState(false);
  useLayoutEffect(() => {if (match) setVisited(true);}, [match]);
  return captured && visited ? <CapturedSavedOwner captured={captured}/> : null;
}

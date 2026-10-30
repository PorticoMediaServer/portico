import {forwardIdentity} from '../detail/context';
import {useLayoutEffect, useState} from 'react';
import {useLocation, useParams} from '@tanstack/react-router';
import {defineWebScreen} from '../../route-registry';
import {useProtectedWebScreenContext} from '../../route-registry-view';
import {useCommittedLibraryScreenContext, type ProtectedLibraryScreen} from '../browse/screen-context';
import {router} from '../../router';
import {createPersonBindings, type PersonBindings} from './person-bindings';
import type {ScreenContext} from './context';
import {DetailLoading} from './Loading';
import {PersonScreen} from './Person';

function BoundPerson({captured}: {captured: ProtectedLibraryScreen}) {
  const {personId} = useParams({from: '/person/$personId'}), href = useLocation({select: location => location.href});
  const context = useCommittedLibraryScreenContext(captured, href, () => router.state.location.href === href);
  const runtime = captured.binding.playback?.runtime;
  const [published, publish] = useState<Readonly<{context: ScreenContext; personId: string; runtime: typeof runtime; binding: PersonBindings; current: () => boolean}>>();
  useLayoutEffect(() => {
    if (!context) return;
    let live = true;
    const current = () => live && context.isCurrent();
    const binding = createPersonBindings(forwardIdentity(context, {isCurrent: current}));
    publish({context, personId, runtime, binding, current});
    void binding.select(personId).catch(error => {if (current()) context.effects.reportError(error);});
    return () => {live = false;binding.dispose();};
  }, [context, personId, runtime]);
  const owned = context && published && published.context === context && published.personId === personId
    && published.runtime === runtime && published.current() ? published : undefined;
  return owned ? <PersonScreen binding={owned.binding} personId={personId}/> : <DetailLoading/>;
}
function PersonRouteScreen() {
  const captured = useProtectedWebScreenContext();
  return captured ? <BoundPerson captured={captured}/> : null;
}
export default defineWebScreen({id: 'person', component: PersonRouteScreen});

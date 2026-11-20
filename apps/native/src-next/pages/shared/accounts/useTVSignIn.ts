import {useEffect, useRef} from 'react';
import {useDestinationEntryFocus} from '../../../screens/destinations/useEntryFocus';
import {useTVBusyEntryRevision} from '../../../tv/auth/entry-revision';
import {useCodeRetry} from '../../../tv/auth/code-retry';
import type {TVFrontDoorProps} from '../../tv/accounts/SignInPage';

export function useTVSignIn({busy, autoStart = false, onPortico}: TVFrontDoorProps) {
  const start = useRef({autoStart, onPortico}); start.current = {autoStart, onPortico};
  useEffect(() => {if (start.current.autoStart) start.current.onPortico();}, []);
  useCodeRetry(!busy, onPortico);
  const id = 'tv.auth.front-door';
  const entryRevision = useTVBusyEntryRevision(busy);
  useDestinationEntryFocus({visible: !busy, intent: 'enter', revision: entryRevision}, {regionId: id, childId: 'portico'});
  return id;
}

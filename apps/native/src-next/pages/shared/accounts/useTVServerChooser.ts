import {useCallback, useRef, useState} from 'react';
import {findNodeHandle, type View} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {useDestinationEntryFocus} from '../../../screens/destinations/useEntryFocus';
import {useTVBusyEntryRevision} from '../../../tv/auth/entry-revision';
import type {Snapshot} from './useServerChooser';

/** `embedded` (a TV's Switch server Settings page): Settings' list keeps focus when the page opens; the chooser asks for
 * its start again only after a refresh, switch or sign-out run that detached its controls ends. */
export function useTVServerChooser(snapshot: Snapshot | undefined, busy: boolean, embedded = false) {
  const t = defaultI18n.t;
  const selected = snapshot?.servers.find(row => row.id === snapshot.selectedServerId);
  const groups = [
    {id: 'portico', title: t('servers.yours'), rows: snapshot?.servers.filter(row => row.id !== selected?.id && row.signIn === 'portico') ?? []},
    {id: 'local', title: t('accounts.servers.local'), rows: snapshot?.servers.filter(row => row.id !== selected?.id && row.signIn === 'local') ?? []},
  ];
  const preferred = groups.flatMap(group => group.rows)[0]?.id;
  // Vertical order is the choosable servers only; each Forget is reached sideways.
  const serverOrder = groups.flatMap(group => group.rows).map(value => `server:${value.id}`);
  const entryRevision = useTVBusyEntryRevision(busy);
  // Down from Refresh servers goes to Use a local server beside it, not past it to the footer.
  const [addHandle, bindAdd] = useNativeHandle();
  // Down from the last server goes to Refresh servers, the first of the two actions side by side under it.
  const [refreshHandle, bindRefresh] = useNativeHandle();
  // Embedded, every control is a stop of the one 'accounts.chooser' region (pages/tv/accounts/ServerChooserPage).
  const rowsRegion = embedded ? 'accounts.chooser' : 'accounts.chooser.rows';
  const actionsRegion = embedded ? 'accounts.chooser' : 'accounts.chooser.actions';
  useDestinationEntryFocus({visible: !busy && (!embedded || entryRevision > 0), intent: 'enter', revision: entryRevision},
    preferred ? {regionId: rowsRegion, childId: `server:${preferred}`} : {regionId: actionsRegion, childId: 'refresh'});
  // In Settings, Left from a server's Sign out or Forget stays in its row (the region's own Left goes to Settings' list).
  const [handles, setHandles] = useState<Readonly<Record<string, number | undefined>>>({});
  // One callback per control, kept across renders: a new one each render would detach and reattach it every time.
  const binders = useRef(new Map<string, (view: View | null) => void>());
  const bindHandle = useCallback((id: string) => {
    let bind = binders.current.get(id);
    if (!bind) {
      bind = (view: View | null) => {
        const handle = view === null ? undefined : findNodeHandle(view) ?? undefined;
        setHandles(current => current[id] === handle ? current : {...current, [id]: handle});
      };
      binders.current.set(id, bind);
    }
    return bind;
  }, []);
  return {selected, groups, preferred, serverOrder, addHandle, bindAdd, refreshHandle, bindRefresh, handles, bindHandle};
}

/** A control's native handle, for a declared next-focus aimed at it. */
function useNativeHandle() {
  const [handle, setHandle] = useState<number | undefined>(undefined);
  const bind = useCallback((view: View | null) => {
    setHandle(view === null ? undefined : findNodeHandle(view) ?? undefined);
  }, []);
  return [handle, bind] as const;
}

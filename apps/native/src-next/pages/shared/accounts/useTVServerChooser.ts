import {useCallback, useState} from 'react';
import {findNodeHandle, type View} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {useDestinationEntryFocus} from '../../../screens/destinations/useEntryFocus';
import {useTVBusyEntryRevision} from '../../../tv/auth/entry-revision';
import type {Snapshot} from './useServerChooser';

export function useTVServerChooser(snapshot: Snapshot | undefined, busy: boolean) {
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
  const [addHandle, setAddHandle] = useState<number | undefined>(undefined);
  const bindAdd = useCallback((view: View | null) => {
    setAddHandle(view === null ? undefined : findNodeHandle(view) ?? undefined);
  }, []);
  useDestinationEntryFocus({visible: !busy, intent: 'enter', revision: entryRevision},
    preferred ? {regionId: 'accounts.chooser.rows', childId: `server:${preferred}`} : {regionId: 'accounts.chooser.actions', childId: 'refresh'});
  return {selected, groups, preferred, serverOrder, addHandle, bindAdd};
}

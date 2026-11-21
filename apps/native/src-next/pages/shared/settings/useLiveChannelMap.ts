import {useEffect, useRef, useState} from 'react';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeLiveOwnerController} from '../../../screens/settings-live/live-owner-controller';
import type {ChannelMapEdit, ChannelMapPage, LiveSource} from '../../../screens/settings-live/live-owner-model';
import type {View} from 'react-native';



export function useLiveChannelMap({controller, runtime, source, open, onOpenChange}: {controller: NativeLiveOwnerController; runtime: NativeShellRuntime; source?: LiveSource; open: boolean; onOpenChange: (open: boolean) => void}) {
  const t = runtime.text.t;
  const [page, setPage] = useState<ChannelMapPage>(), [edits, setEdits] = useState<Record<string, ChannelMapEdit>>({}), [filter, setFilter] = useState(''), [error, setError] = useState<unknown>(), [busy, setBusy] = useState(false), [loading, setLoading] = useState(false);
  const close = useRef<View>(null);
  const load = async (id: string, cursor = '') => {
    setLoading(true); setError(undefined);
    try {
      const next = await controller.channelMap(id, cursor);
      setPage(current => cursor && current ? {...next, items: [...current.items, ...next.items]} : next);
    } catch (failure) {setError(failure);}
    finally {setLoading(false);}
  };
  useEffect(() => {
    if (!open || !source) return;
    setPage(undefined); setEdits({}); setFilter(''); setError(undefined);
    void load(source.id);
  }, [open, source]); // eslint-disable-line react-hooks/exhaustive-deps
  const value = (channelId: string, entry: ChannelMapPage['items'][number]): ChannelMapEdit => edits[channelId] ?? {number: entry.overridden ? entry.number : '', guideChannelId: entry.guideChannelId, hidden: entry.hidden};
  const edit = (entry: ChannelMapPage['items'][number], patch: Partial<ChannelMapEdit>) => setEdits(current => ({...current, [entry.channelId]: {...value(entry.channelId, entry), ...patch}}));
  const changed = Object.keys(edits).length;
  const shown = page ? (filter.trim() ? page.items.filter(entry => `${entry.name} ${entry.number} ${entry.group}`.toLowerCase().includes(filter.trim().toLowerCase())) : page.items) : [];
  const save = async () => {
    if (!source || !changed) return;
    setBusy(true); setError(undefined);
    try {
      await controller.saveChannelMap(source.id, Object.entries(edits).map(([channelId, override]) => ({channelId, override})));
      setEdits({}); setBusy(false);
      await load(source.id);
    } catch (failure) {
      const partial = failure as {saved?: number; total?: number};
      setBusy(false); setError(partial.saved !== undefined ? Object.assign(new Error('partial'), {saved: partial.saved, total: partial.total}) : failure);
      if (source) void load(source.id);
    }
  };
  return {t, close, error, changed, busy, load, filter, setFilter, loading, page, shown, value, edits, edit, save};
}

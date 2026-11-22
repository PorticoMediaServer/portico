import {useEffect, useRef, useState} from 'react';
import type {View} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {importSkipped} from '../../../../../../packages/client-core/src/server-admin/live-words';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {newRemoteDraft, type NativeLiveOwnerController, type RemoteSourceDraft, type RemoteSourcePreview, type SourceKind, type TunerDiscovery} from '../../../screens/settings-live/live-owner-controller';
import type {ChannelMapEdit, ChannelMapPage, LiveSource} from '../../../screens/settings-live/live-owner-model';

export const sourceKinds: readonly SourceKind[] = ['m3u', 'xtream', 'hdhomerun'];
export const refreshOptions = ['3600000', '21600000', '43200000', '86400000'] as const;
export const refreshKey = {'3600000': 'web.libraries.everyHour', '21600000': 'web.libraries.every6h', '43200000': 'web.liveTv.every12h', '86400000': 'web.libraries.daily'} as const;

/** Add a playlist or tuner the way the web does: check the source, show what it found, then save. A tuner found by
 * Discover arrives filled in. A source on the home network asks once before it is read. Moved as it was. */
export function useAddSource({controller, runtime, open, tuner, onSaved}: {controller: NativeLiveOwnerController; runtime: NativeShellRuntime; open: boolean; tuner?: string; onSaved: () => void}) {
  const t = runtime.text.t;
  const [draft, setDraft] = useState<RemoteSourceDraft>(() => newRemoteDraft(controller.lifetime.authority.profileId));
  const [preview, setPreview] = useState<RemoteSourcePreview>(), [confirm, setConfirm] = useState<readonly string[]>(), [busy, setBusy] = useState(false), [error, setError] = useState<unknown>();
  const cancel = useRef<View>(null);
  useEffect(() => {
    if (!open) return;
    setDraft({...newRemoteDraft(controller.lifetime.authority.profileId), ...(tuner ? {kind: 'hdhomerun' as SourceKind, locator: tuner, name: t('server.live.kind.hdhomerun'), useDiscoveredCapacity: true} : {})});
    setPreview(undefined); setConfirm(undefined); setError(undefined); setBusy(false);
  }, [open, tuner, controller, t]);
  const update = (patch: Partial<RemoteSourceDraft>) => {setDraft(current => ({...current, ...patch})); setPreview(undefined);};
  const ready = draft.name.trim().length > 0 && draft.locator.trim().length > 0;
  const check = async (next: RemoteSourceDraft) => {
    setBusy(true); setError(undefined);
    try {
      setPreview(await controller.previewRemote(next));
      setConfirm(undefined);
    } catch (failure) {
      const roots = (failure as {confirmRoots?: unknown}).confirmRoots;
      if (Array.isArray(roots) && roots.length && roots.every(value => typeof value === 'string')) setConfirm(roots as string[]);
      else setError(failure);
    } finally {setBusy(false);}
  };
  const save = async () => {
    if (!preview) return;
    setBusy(true); setError(undefined);
    const result = await controller.saveRemote(preview.previewId);
    setBusy(false);
    if (result) onSaved();
  };
  const skipped = preview ? importSkipped(t, id => defaultI18n.has(id), preview.preview.diagnostics) : undefined;
  return {t, draft, update, ready, busy, error, clearError: () => setError(undefined), cancel, confirm,
    found: preview && {facts: [t('server.live.channelCount', {count: preview.preview.channels}), t('server.live.programCount', {count: preview.preview.programmes}), ...(preview.discoveredCapacity ? [t('web.liveTv.capacityTuners', {count: preview.discoveredCapacity})] : [])].filter(Boolean),
      names: `${preview.preview.names.slice(0, 8).join(', ')}${preview.preview.names.length > 8 ? '…' : ''}`, skipped: skipped ? [skipped.summary, ...skipped.reasons].join(' ') : undefined},
    step: confirm ? 'confirm' as const : preview ? 'save' as const : 'check' as const,
    confirmLan: () => {if (!confirm) return; const next = {...draft, confirmedLanRoots: [...new Set([...draft.confirmedLanRoots, ...confirm])]}; setDraft(next); void check(next);},
    save: () => {void save();}, check: () => {void check(draft);},
    locatorLabel: draft.kind === 'hdhomerun' ? t('web.liveTv.tunerAddress') : draft.kind === 'xtream' ? t('web.liveTv.serverAddress') : t('web.liveTv.playlistUrl')};
}

/** Looking for tuners never adds one by itself: Use this hands its address to Add source. */
export function useDiscover({controller, open}: {controller: NativeLiveOwnerController; open: boolean}) {
  const [result, setResult] = useState<TunerDiscovery>(), [busy, setBusy] = useState(false), [error, setError] = useState<unknown>();
  const search = async () => {
    setBusy(true); setError(undefined);
    try {setResult(await controller.discover());}
    catch (failure) {setError(failure);}
    finally {setBusy(false);}
  };
  useEffect(() => {if (open) {setResult(undefined); void search();}}, [open]); // eslint-disable-line react-hooks/exhaustive-deps
  const close = useRef<View>(null);
  return {result, busy, error, close, search: () => {void search();}};
}

type MapEntry = ChannelMapPage['items'][number];

/** A source's channels and numbers: hide a channel, give it a number or a guide id; only touched channels are sent, and
 * a save that lands part-way says how many were saved. */
export function useChannelMap({controller, source, open}: {controller: NativeLiveOwnerController; source?: LiveSource; open: boolean}) {
  const [page, setPage] = useState<ChannelMapPage>(), [edits, setEdits] = useState<Record<string, ChannelMapEdit>>({}), [filter, setFilter] = useState(''), [error, setError] = useState<unknown>(), [busy, setBusy] = useState(false), [loading, setLoading] = useState(false);
  const close = useRef<View>(null);
  // A reload after a save that stopped part-way keeps its "N of M saved" message (keep).
  const load = async (id: string, cursor = '', keep = false) => {
    setLoading(true); if (!keep) setError(undefined);
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
  const value = (channelId: string, entry: MapEntry): ChannelMapEdit => edits[channelId] ?? {number: entry.overridden ? entry.number : '', guideChannelId: entry.guideChannelId, hidden: entry.hidden};
  const edit = (entry: MapEntry, patch: Partial<ChannelMapEdit>) => setEdits(current => ({...current, [entry.channelId]: {...value(entry.channelId, entry), ...patch}}));
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
      if (source) void load(source.id, '', true);
    }
  };
  const partial = error as {saved?: number; total?: number} | undefined;
  return {page, filter, setFilter, error, partial: error !== undefined && partial?.saved !== undefined ? {saved: partial.saved ?? 0, total: partial.total ?? changed} : undefined,
    retry: () => {if (source) void load(source.id);}, busy, loading, close, changed, save: () => {void save();},
    more: page?.nextCursor ? () => {if (source && page) void load(source.id, page.nextCursor);} : undefined,
    rows: shown.map(entry => ({entry, value: value(entry.channelId, entry), touched: edits[entry.channelId] !== undefined, edit: (patch: Partial<ChannelMapEdit>) => edit(entry, patch)}))};
}

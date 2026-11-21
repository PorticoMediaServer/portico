import {useEffect, useRef, useState} from 'react';
import type {View} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {importSkipped} from '../../../../../../packages/client-core/src/server-admin/live-words';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {newRemoteDraft, type NativeLiveOwnerController, type RemoteSourceDraft,
  type RemoteSourcePreview, type SourceKind, type TunerDiscovery} from '../../../screens/settings-live/live-owner-controller';

export function useAddLiveSource({controller, runtime, open, tuner, onSaved}: {controller: NativeLiveOwnerController; runtime: NativeShellRuntime; open: boolean; tuner?: string; onSaved: () => void}) {
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
  const confirmLan = () => {
    if (!confirm) return;
    const next = {...draft, confirmedLanRoots: [...new Set([...draft.confirmedLanRoots, ...confirm])]};
    setDraft(next);
    void check(next);
  };
  const skipped = preview ? importSkipped(t, id => defaultI18n.has(id), preview.preview.diagnostics) : undefined;
  return {t, draft, preview, confirm, busy, error, setError, cancel, update, ready, check, save, skipped, confirmLan};
}

export function useDiscoverLiveTuners({controller, runtime, open}: {controller: NativeLiveOwnerController; runtime: NativeShellRuntime; open: boolean}) {
  const t = runtime.text.t;
  const [result, setResult] = useState<TunerDiscovery>(), [busy, setBusy] = useState(false), [error, setError] = useState<unknown>();
  const search = async () => {
    setBusy(true); setError(undefined);
    try {setResult(await controller.discover());}
    catch (failure) {setError(failure);}
    finally {setBusy(false);}
  };
  useEffect(() => {if (open) {setResult(undefined); void search();}}, [open]); // eslint-disable-line react-hooks/exhaustive-deps
  const close = useRef<View>(null);
  return {t, result, busy, error, search, close};
}

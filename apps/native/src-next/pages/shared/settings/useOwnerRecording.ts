import {useCallback, useEffect, useMemo, useRef, useState} from 'react';
import type {DvrFolderStatus, DvrOverview, DvrTuners} from '../../../../../../packages/contracts/src/next/api';
import {DVR_KEEP_PRESETS, DVR_PADDING_PRESETS, DVR_SPACE_PRESETS, matchingDvrPreset, readDvrFolder, readDvrTuners, readOwnerDvrSettings, readRecordingStorage, saveOwnerDvrSettings, type OwnerDvrDocument, type OwnerDvrSettings} from '../../../../../../packages/client-core/src/server-admin/owner-dvr-settings';
import {readRecordingPermissions, saveRecordingPermission, type DvrPermission} from '../../../../../../packages/client-core/src/server-admin/owner-recording-permissions';
import {readOwnerRecordingRules, readRecordingRuleProgrammes, removeOwnerRecordingRule, saveOwnerRecordingRule, type DvrRule, type RecordingRuleProgramme} from '../../../../../../packages/client-core/src/server-admin/owner-recording-rules';
import {withGuardedRaw} from '../../../../../../packages/client-core/src/home-selection/list-destinations';
import type {LibraryContentApi} from '../../../../../../packages/client-core/src/library-content';
import {defaultI18n, type MessageId} from '../../../../../../packages/i18n/src';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import {screenScopeEnded} from '../../../screens/settings-live/lifetime';
import {useSettingsRequestScope} from '../../../screens/settings-live/use-settings-request-scope';

type Translate = typeof defaultI18n.t;

export function useOwnerRecording(context: NativeShellBootstrapContext, runtime: NativeShellRuntime) {
  const t = runtime.text.t as Translate;
  const {api, lifetime} = useSettingsRequestScope(context);
  const rebuilt = useMemo(() => withGuardedRaw(api, context.api, () => {if (!lifetime.current()) throw screenScopeEnded();}) as unknown as LibraryContentApi, [api, context, lifetime]);
  const [dvr, setDvr] = useState<OwnerDvrDocument>(), [overview, setOverview] = useState<DvrOverview>(), [folder, setFolder] = useState<DvrFolderStatus>(), [tuners, setTuners] = useState<DvrTuners>();
  const [permissions, setPermissions] = useState<readonly DvrPermission[]>([]), [rules, setRules] = useState<readonly DvrRule[]>([]);
  const [busy, setBusy] = useState(false), [error, setError] = useState<unknown>(), [choosing, setChoosing] = useState(false), [removing, setRemoving] = useState<DvrRule>(), [cancelBookings, setCancelBookings] = useState(false), [adding, setAdding] = useState(false);
  const load = useCallback(() => {
    const keep = <T,>(set: (value: T) => void) => (value: T) => {if (lifetime.current()) set(value);};
    const fail = (failure: unknown) => {if (lifetime.current()) setError(failure);};
    void readOwnerDvrSettings(rebuilt).then(keep(setDvr), fail);
    void readRecordingStorage(rebuilt).then(keep(setOverview), fail);
    void readDvrFolder(rebuilt).then(keep(setFolder), fail);
    void readDvrTuners(rebuilt).then(keep(setTuners), fail);
    void readRecordingPermissions(rebuilt).then(keep(setPermissions), fail);
    void readOwnerRecordingRules(rebuilt).then(keep(setRules), fail);
  }, [rebuilt, lifetime]);
  useEffect(load, [load]);
  const flight = useRef(false);
  const run = async (work: () => Promise<unknown>) => {
    if (flight.current || !lifetime.current()) return;
    flight.current = true;
    setBusy(true); setError(undefined);
    try {await work();} catch (failure) {if (lifetime.current()) setError(failure);} finally {flight.current = false; if (lifetime.current()) {setBusy(false); load();}}
  };
  const saveDvr = (change: Partial<OwnerDvrSettings>) => {if (dvr) void run(async () => setDvr(await saveOwnerDvrSettings(rebuilt, dvr, change)));};
  const choices = (row: string, values: readonly string[]) => values.map((value, index) => ({value, label: t(`${row}.control.options.${index}.label` as MessageId), disabled: value === 'custom'}));
  const v = dvr?.value;
  const padding = v ? matchingDvrPreset(DVR_PADDING_PRESETS, v) : 'custom', keep = v ? matchingDvrPreset(DVR_KEEP_PRESETS, v) : 'custom', space = v ? matchingDvrPreset(DVR_SPACE_PRESETS, v) : 'custom';
  const accounts = new Map<string, DvrPermission[]>();
  for (const row of permissions) accounts.set(row.accountId, [...(accounts.get(row.accountId) ?? []), row]);
  const permission = (row: DvrPermission, enabled: boolean, profile: boolean) => {
    void run(async () => {
      const next = await saveRecordingPermission(rebuilt, {accountId: row.accountId, enabled,
        ...(profile ? {profileId: row.profileId, revision: row.revision} : {revision: row.accountRevision})});
      if (lifetime.current()) setPermissions(next);
    });
  };
  const toggleRule = (rule: DvrRule) => {void run(() => saveOwnerRecordingRule(rebuilt, {paused: !rule.paused}, rule.id));};
  const removeRule = () => {
    if (!removing) return;
    void run(async () => {
      await removeOwnerRecordingRule(rebuilt, removing.id, cancelBookings);
      if (lifetime.current()) setRemoving(undefined);
    });
  };
  return {t, rebuilt, folder, tuners, overview, rules, accounts, busy, error, choosing, setChoosing, removing, setRemoving,
    cancelBookings, setCancelBookings, adding, setAdding, load, saveDvr, v, padding, keep, space, choices, permission, toggleRule, removeRule};
}

export function useNewRecordingRule({context, runtime, open, onSaved}: {
  context: NativeShellBootstrapContext; runtime: NativeShellRuntime; open: boolean; onSaved: () => void;
}) {
  const t = runtime.text.t as Translate;
  const {api, lifetime} = useSettingsRequestScope(context);
  const [owners, setOwners] = useState<readonly DvrPermission[]>([]), [profileId, setProfileId] = useState(''), [day, setDay] = useState(() => new Date().toISOString().slice(0, 10));
  const [programmes, setProgrammes] = useState<readonly RecordingRuleProgramme[]>([]), [cursor, setCursor] = useState<string>(), [programme, setProgramme] = useState(''), [newOnly, setNewOnly] = useState(false);
  const [busy, setBusy] = useState(false), [error, setError] = useState<unknown>();
  useEffect(() => {
    if (!open) return;
    setProfileId(''); setProgramme(''); setNewOnly(false); setError(undefined);
    void readRecordingPermissions(api).then(rows => {if (lifetime.current()) setOwners(rows.filter(row => row.canRecord));}, failure => {if (lifetime.current()) setError(failure);});
  }, [open, api, lifetime]);
  const guide = useCallback((more?: string) => {
    void readRecordingRuleProgrammes(api, day, '', more).then(page => {if (lifetime.current()) {setProgrammes(current => more ? [...current, ...page.items] : page.items); setCursor(page.nextCursor);}}, failure => {if (lifetime.current()) setError(failure);});
  }, [api, day, lifetime]);
  useEffect(() => {if (open) {setProgramme(''); guide();}}, [open, guide]);
  const flight = useRef(false);
  const save = async () => {
    if (flight.current || !open || !profileId || !programme || !lifetime.current()) return;
    flight.current = true;
    setBusy(true); setError(undefined);
    try {await saveOwnerRecordingRule(api, {profileId, programme, newOnly}); if (lifetime.current()) onSaved();}
    catch (failure) {if (lifetime.current()) setError(failure);}
    finally {flight.current = false; if (lifetime.current()) setBusy(false);}
  };
  const days = Array.from({length: 7}, (_, index) => {const date = new Date(Date.now() + index * 86_400_000); return {value: date.toISOString().slice(0, 10), label: defaultI18n.formatDate(date.toISOString(), {weekday: 'short', day: 'numeric', month: 'short'})};});
  return {t, owners, profileId, setProfileId, day, setDay, programmes, cursor, programme, setProgramme, newOnly, setNewOnly,
    busy, error, guide, save, days};
}

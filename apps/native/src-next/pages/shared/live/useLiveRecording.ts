import {useCallback, useEffect, useMemo, useState} from 'react';
import type {DvrFolderStatus, DvrOverview, DvrTuners} from '../../../../../../packages/contracts/src/next/api';
import {DVR_KEEP_PRESETS, DVR_PADDING_PRESETS, DVR_SPACE_PRESETS, matchingDvrPreset, readDvrFolder, readDvrTuners, readOwnerDvrSettings, readRecordingStorage, saveOwnerDvrSettings, type OwnerDvrDocument, type OwnerDvrSettings} from '../../../../../../packages/client-core/src/server-admin/owner-dvr-settings';
import {readRecordingPermissions, saveRecordingPermission, type DvrPermission} from '../../../../../../packages/client-core/src/server-admin/owner-recording-permissions';
import {readOwnerRecordingRules, readRecordingRuleProgrammes, removeOwnerRecordingRule, saveOwnerRecordingRule, type DvrRule, type RecordingRuleProgramme} from '../../../../../../packages/client-core/src/server-admin/owner-recording-rules';
import {tunerStatus} from '../../../../../../packages/client-core/src/server-admin/live-words';
import {withGuardedRaw} from '../../../../../../packages/client-core/src/home-selection/list-destinations';
import type {LibraryContentApi} from '../../../../../../packages/client-core/src/library-content';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import {defaultI18n, type MessageId} from '../../../../../../packages/i18n/src';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import {screenScopeEnded} from '../../../screens/settings-live/lifetime';
import {useSettingsRequestScope} from '../../../screens/settings-live/use-settings-request-scope';

type Translate = typeof defaultI18n.t;
const G = 'next.settingsSpec.server.live.groups';
const errorText = (error: unknown) => error === undefined ? undefined : presentError(error, 'server-console', {operation: 'action'}).body;

/** Settings › Server › Live TV › Recording: tuners and what they are doing, who may record, recording rules, recording
 * defaults, the recordings folder, and how long recordings are kept and how much space they may use. The same reads and
 * saves web's Recording page uses; each choice is saved when it is made, at the revision read. Moved from the old screen. */
export function useLiveRecording({context, runtime}: {context: NativeShellBootstrapContext; runtime: NativeShellRuntime}) {
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
  const run = async (work: () => Promise<unknown>) => {
    if (busy || !lifetime.current()) return;
    setBusy(true); setError(undefined);
    try {await work();} catch (failure) {if (lifetime.current()) setError(failure);} finally {if (lifetime.current()) {setBusy(false); load();}}
  };
  const saveDvr = (change: Partial<OwnerDvrSettings>) => {if (dvr) void run(async () => setDvr(await saveOwnerDvrSettings(rebuilt, dvr, change)));};
  const choices = (row: string, values: readonly string[]) => values.map((value, index) => ({value, label: t(`${row}.control.options.${index}.label` as MessageId), disabled: value === 'custom'}));
  const v = dvr?.value;
  const accounts = new Map<string, DvrPermission[]>();
  for (const row of permissions) accounts.set(row.accountId, [...(accounts.get(row.accountId) ?? []), row]);
  const tunerRows = (tuners?.sources ?? []).map(source => ({key: source.sourceId, name: source.sourceName, status: tunerStatus(t, source),
    using: [source.paused ? t('web.liveTv.pausedBadge') : '', ...source.active.map(use => `${use.using} · ${use.channel}`)].filter(Boolean).join('\n') || undefined}));
  const accountGroups = [...accounts].map(([id, rows]) => {
    const account = rows[0]!;
    return {key: id, name: account.accountName, help: t(account.ownerAccount ? 'web.liveTv.ownerMayRecord' : 'web.liveTv.accountMayRecord'), allowed: account.accountAllowed, locked: account.ownerAccount,
      set: (enabled: boolean) => {void run(async () => setPermissions(await saveRecordingPermission(rebuilt, {accountId: account.accountId, enabled, revision: account.accountRevision})));},
      profiles: rows.map(row => ({key: row.profileId, name: row.profileName, help: t(row.canRecord ? 'next.liveOwner.canRecord' : 'next.liveOwner.cannotRecord'),
        on: row.accountAllowed && row.profileAllowed, locked: !row.accountAllowed,
        set: (enabled: boolean) => {void run(async () => setPermissions(await saveRecordingPermission(rebuilt, {accountId: row.accountId, profileId: row.profileId, enabled, revision: row.revision})));}}))};
  });
  const ruleRows = rules.map(rule => ({key: rule.id, title: rule.title, facts: [rule.profileName, rule.channelName, rule.paused ? t('web.recordingRules.pausedRule') : ''].filter(Boolean).join(' · '),
    pauseLabel: t(rule.paused ? 'web.recordingRules.resumeRule' : 'web.recordingRules.pauseRule'),
    togglePause: () => {void run(() => saveOwnerRecordingRule(rebuilt, {paused: !rule.paused}, rule.id));},
    remove: () => {setCancelBookings(false); setRemoving(rule);}}));
  const defaults = v && {
    extraTime: {label: t(`${G}.6.rows.0.label` as MessageId), value: matchingDvrPreset(DVR_PADDING_PRESETS, v), options: choices(`${G}.6.rows.0`, ['standard', 'exact', 'generous', 'sport', 'custom']),
      choose: (value: string) => {if (value in DVR_PADDING_PRESETS) saveDvr(DVR_PADDING_PRESETS[value as keyof typeof DVR_PADDING_PRESETS]);}},
    conflict: {value: v.stopViewers ? 'stop-watching' : 'keep-watching', options: [{value: 'keep-watching', label: t('server.live.tunerConflict.keep')}, {value: 'stop-watching', label: t('server.live.tunerConflict.stop')}],
      choose: (value: string) => saveDvr({stopViewers: value === 'stop-watching'})},
    folder: [folder?.folder ?? v.folder, folder?.state === 'unwritable' ? `${t('web.liveTv.notWritable')}${folder.problem ? ` ${folder.problem}` : ''}` : folder?.state === 'not_made' ? t('web.liveTv.recordingFolderNotMade') : ''].filter(Boolean).join('\n'),
    keep: {label: t(`${G}.6.rows.2.label` as MessageId), value: matchingDvrPreset(DVR_KEEP_PRESETS, v), options: choices(`${G}.6.rows.2`, ['all', 'd30', 'd90', 'n5', 'n10', 'custom']),
      choose: (value: string) => {if (value in DVR_KEEP_PRESETS) saveDvr(DVR_KEEP_PRESETS[value as keyof typeof DVR_KEEP_PRESETS]);}},
    space: {label: t(`${G}.7.rows.1.label` as MessageId), value: matchingDvrPreset(DVR_SPACE_PRESETS, v),
      options: ['free10', 'up250', 'up500', 'up1000', 'custom'].map(value => ({value, disabled: value === 'custom', label: value === 'custom' ? t(`${G}.7.rows.1.control.options.4.label` as MessageId)
        : value === 'free10' ? t('web.liveTv.leaveFreeAmount', {size: defaultI18n.bytes(10 * 1024 ** 3)}) : t('web.liveTv.capAmount', {size: defaultI18n.bytes(DVR_SPACE_PRESETS[value as 'up250'].capBytes)})})),
      choose: (value: string) => {if (value in DVR_SPACE_PRESETS) saveDvr(DVR_SPACE_PRESETS[value as keyof typeof DVR_SPACE_PRESETS]);}},
    overview: overview && [t('next.live.keepRule', {rule: overview.keepRule}), overview.folderOk ? t('web.dvr.hoursLeft', {count: overview.hoursLeft}) : ''].filter(Boolean).join(' · '),
    useFolder: (path: string) => saveDvr({folder: path}),
  };
  const confirmRemove = () => {const rule = removing; if (rule) void run(async () => {await removeOwnerRecordingRule(rebuilt, rule.id, cancelBookings); setRemoving(undefined);});};
  return {t, api: rebuilt, busy, error: errorText(error), tuners: tuners && tunerRows, accountGroups, rules: ruleRows, defaults, choosing, setChoosing, adding, setAdding, load,
    removing, closeRemove: () => {if (!busy) setRemoving(undefined);}, cancelBookings, setCancelBookings, confirmRemove};
}

/** A new recording rule on the rebuilt server: the owner chooses a profile that may record and a programme from the
 * guide; the server makes the series (or title) rule for it (`POST /v1/dvr/rules`, client-core saveOwnerRecordingRule). */
export function useNewRule({context, runtime, open, onSaved}: {context: NativeShellBootstrapContext; runtime: NativeShellRuntime; open: boolean; onSaved: () => void}) {
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
  const save = async () => {
    if (busy || !profileId || !programme) return;
    setBusy(true); setError(undefined);
    try {await saveOwnerRecordingRule(api, {profileId, programme, newOnly}); if (lifetime.current()) onSaved();}
    catch (failure) {if (lifetime.current()) setError(failure);}
    finally {if (lifetime.current()) setBusy(false);}
  };
  const days = Array.from({length: 7}, (_, index) => {const date = new Date(Date.now() + index * 86_400_000); return {value: date.toISOString().slice(0, 10), label: defaultI18n.formatDate(date.toISOString(), {weekday: 'short', day: 'numeric', month: 'short'})};});
  return {t, busy, error: errorText(error), profileId, setProfileId, day, setDay, days, programme, setProgramme, newOnly, setNewOnly, more: cursor ? () => guide(cursor) : undefined,
    profiles: [{value: '', label: t('web.recordingRules.chooseProfile')}, ...owners.map(row => ({value: row.profileId, label: `${row.accountName} · ${row.profileName}`}))],
    programmes: [{value: '', label: t('web.recordingRules.chooseProgram')}, ...programmes.map(row => ({value: row.id, label: `${row.title ?? ''} · ${row.channelName} · ${defaultI18n.formatDate(row.startsAt, {hour: 'numeric', minute: '2-digit'})}`}))],
    canSave: !busy && !!profileId && !!programme, save: () => {void save();}};
}

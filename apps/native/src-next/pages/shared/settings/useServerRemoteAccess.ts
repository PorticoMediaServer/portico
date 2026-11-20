import {useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {OwnerRemoteSettings} from '../../../../../../packages/client-core/src/server-admin/remote-settings';
import {withGuardedRaw} from '../../../../../../packages/client-core/src/home-selection/list-destinations';
import {settingsSpec, type SettingsRow as SpecRow} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import {screenScopeEnded} from '../../../screens/settings-live/lifetime';
import {useSettingsRequestScope} from '../../../screens/settings-live/use-settings-request-scope';

type OwnAddress = {enabled: boolean; domain: string; certificateFile: string; keyFile: string};
const when = (value: string) => defaultI18n.dateTime(value);
const section = settingsSpec.find(value => value.scope === 'server' && value.id === 'remote');
const main = section?.groups ?? [], extra = section?.advanced?.groups ?? [];
type Group = (typeof main)[number];
const ownAddressEdit = 'remote.ownCertificate';

/** Keep revision-bearing writes and lockout confirmation in the owner request scope. */
export function useServerRemoteAccess({ports, runtime}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime}) {
  const {context} = ports, {t} = runtime.text;
  const {api, lifetime} = useSettingsRequestScope(context);
  const source = useMemo(() => new OwnerRemoteSettings(withGuardedRaw(api, context.api, () => {if (!lifetime.current()) throw screenScopeEnded();}), lifetime.current), [api, context, lifetime]);
  useEffect(() => () => source.dispose(), [source]);
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot), status = state.status;
  const [loaded, setLoaded] = useState(false), [loadError, setLoadError] = useState<unknown>(), [, setRevision] = useState(0);
  const [drafts, setDrafts] = useState<ReadonlyMap<string, unknown>>(new Map()), [saving, setSaving] = useState(false), [saveError, setSaveError] = useState<unknown>();
  const confirmationTarget = useRef<{source: OwnerRemoteSettings; id: string} | undefined>(undefined);
  const [own, setOwn] = useState<OwnAddress>(), [advanced, setAdvanced] = useState(false);
  const reread = useCallback(async () => {await source.readConfiguration(); if (lifetime.current()) {setLoaded(true); setRevision(value => value + 1);}}, [source, lifetime]);
  const load = useCallback(() => {void reread().then(() => {if (lifetime.current()) setLoadError(undefined);}, error => {if (lifetime.current()) setLoadError(error);});}, [reread, lifetime]);
  useEffect(load, [load]);
  const value = (id: string) => drafts.has(id) ? drafts.get(id) : source.read(id)?.current;
  const enabled = (row: SpecRow) => {
    const rule = row.enableWhen; if (!rule) return true;
    const current = value(rule.id) as never;
    return (!rule.in || rule.in.includes(current)) && (!rule.notIn || !rule.notIn.includes(current));
  };
  const change = (id: string, next: unknown) => {if (!saving) {setSaveError(undefined); setDrafts(current => new Map(current).set(id, next));}};
  const blocked = saving || state.busy || !!state.confirmation;
  const removeDraft = (id: string) => setDrafts(current => {
    const next = new Map(current);
    next.delete(id);
    return next;
  });
  const save = async () => {
    if (blocked || !lifetime.current()) return;
    let writing: string | undefined;
    setSaving(true); setSaveError(undefined);
    try {
      for (const row of [...main, ...extra].flatMap(function all(group: Group | SpecRow): SpecRow[] {return 'rows' in group ? group.rows.flatMap(all) : [group, ...(group.children ?? []).flatMap(all)];})) {
        if (!drafts.has(row.id)) continue;
        writing = row.id;
        await source.save(row.id, drafts.get(row.id));
        if (lifetime.current()) removeDraft(row.id);
        writing = undefined;
        await reread();
      }
    } catch (error) {
      if (lifetime.current()) {
        if (writing && source.getSnapshot().confirmation) confirmationTarget.current = {source, id: writing};
        else setSaveError(error);
      }
      await reread().catch(() => {});
    } finally {if (lifetime.current()) setSaving(false);}
  };

  const cancelConfirmation = () => {
    if (source.getSnapshot().busy) return;
    confirmationTarget.current = undefined;
    source.cancelConfirmation();
  };
  const confirm = async () => {
    if (!source.getSnapshot().confirmation || source.getSnapshot().busy) return;
    const target = confirmationTarget.current;
    const done = await source.confirm();
    if (!lifetime.current()) return;
    if (!source.getSnapshot().confirmation) confirmationTarget.current = undefined;
    if (!done) return;
    if (target?.source === source && target.id === ownAddressEdit) setOwn(undefined);
    else if (target?.source === source) removeDraft(target.id);
    setRevision(value => value + 1);
  };

  const savedOwn = state.interfaces?.document.ownAddress as OwnAddress | undefined, ownDraft = own ?? savedOwn;
  const saveOwn = async () => {
    if (!ownDraft || blocked || !lifetime.current()) return;
    const done = await source.saveOwnAddress(ownDraft);
    if (!lifetime.current()) return;
    if (done) setOwn(undefined);
    else if (source.getSnapshot().confirmation) confirmationTarget.current = {source, id: ownAddressEdit};
  };
  const outside = status?.outside, answers = outside?.addresses ?? [];
  const reachable = answers.length > 0 && answers.every(item => item.reachable);
  const outsideTone = !state.checking && outside?.checkedAt && answers.length ? reachable ? 'success' : 'warning' : 'info';
  const outsideTitle = t(state.checking ? 'settings.server.remote.checking' : !outside?.checkedAt ? 'next.remoteOwner.notChecked'
    : reachable ? 'next.remoteOwner.reachable' : answers.length ? 'next.remoteOwner.unreachable' : 'next.remoteOwner.unknown');
  const certificate = status?.managedCertificate;
  const certificateFacts = certificate ? [t(certificate.ready ? 'next.remoteOwner.serving' : 'next.remoteOwner.waiting'), certificate.name, certificate.problem]
    .filter(Boolean).join(' · ') : undefined;
  const notes = [outside?.problem, status?.remoteSecurity.message, status?.remoteSecurity.problem, state.interfaces?.document.warning,
    state.interfaces?.document.ipv6Note, ...(status?.warnings.map(warning => warning.message) ?? [])]
    .filter((line): line is string => typeof line === 'string' && line.length > 0);
  const outsideFacts = [
    ...(outside?.checkedAt ? [t('web.servers.checked', {when: when(outside.checkedAt)})] : []),
    ...answers.map(item => `${item.address}:${item.port} · ${t(item.reachable ? 'next.remoteOwner.reachable' : 'next.remoteOwner.unreachable')}`),
    ...(certificateFacts ? [`${t('next.remoteStatus.secure')}: ${certificateFacts}`] : []),
    ...[status?.router, status?.routerIPv6].flatMap(router => router && router.state !== 'off'
      ? [`${t('next.remoteOwner.router')}: ${t(router.state === 'open' ? 'next.remoteOwner.mapped' : 'next.remoteOwner.notMapped')}`] : []),
  ];
  return {
    t, when, source, state, status, main, extra, value, change, enabled, blocked, loaded, load, loadError, ownDraft: savedOwn ? ownDraft : undefined, setOwn, saveOwn,
    ownChanged: JSON.stringify(ownDraft) !== JSON.stringify(savedOwn), advanced, setAdvanced, saving, save, saveError, confirm, cancelConfirmation,
    hasDrafts: drafts.size > 0, discard: () => {setDrafts(new Map()); setSaveError(undefined);},
    outsideTitle, outsideFacts, outsideTone, notes,
    actionError: state.error !== undefined && !state.confirmation ? presentError(state.error, 'server-console', {operation: 'action'}).body : undefined,
  };
}

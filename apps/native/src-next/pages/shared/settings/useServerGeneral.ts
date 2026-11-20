import {useCallback, useEffect, useMemo, useState} from 'react';
import {serverNow} from '../../../../../../packages/client-core/src/server-clock';
import {checkServerUpdates, latestVersionLine, readServerUpdates, type ServerUpdates} from '../../../../../../packages/client-core/src/server-admin/server-updates';
import {OwnerGeneralSettings, readBackgroundPause, setBackgroundPause} from '../../../../../../packages/client-core/src/server-admin/general-settings';
import {withGuardedRaw} from '../../../../../../packages/client-core/src/home-selection/list-destinations';
import {restartServer} from '../../../../../../packages/client-core/src/backups';
import type {SettingsConfiguration} from '../../../../../../packages/client-core/src/settings-configuration';
import {settingsSpec, type SettingsRow as SpecRow} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import {when} from '../../../../../../packages/client-core/src/server-admin/console-words';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {screenScopeEnded} from '../../../screens/settings-live/lifetime';
import {useSettingsRequestScope} from '../../../screens/settings-live/use-settings-request-scope';
import {settingsSummary} from '../../../screens/settings-live/server/settings-values';

const section = settingsSpec.find(value => value.scope === 'server' && value.id === 'general');
const main = section?.groups ?? [], extra = section?.advanced?.groups ?? [];

export function useServerGeneral({ports, runtime}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime}) {
  const {context} = ports, {t} = runtime.text;
  const {api, lifetime} = useSettingsRequestScope(context);
  // The documents' revisions travel in headers, so the reader takes the app's header-sending call; it ends with the page.
  const settings = useMemo(() => new OwnerGeneralSettings(withGuardedRaw(api, context.api, () => {if (!lifetime.current()) throw screenScopeEnded();})), [api, context, lifetime]);
  const [configuration, setConfiguration] = useState<SettingsConfiguration>(), [loadError, setLoadError] = useState<unknown>();
  const [drafts, setDrafts] = useState<ReadonlyMap<string, unknown>>(new Map()), [saving, setSaving] = useState(false), [saveError, setSaveError] = useState<unknown>();
  const [updates, setUpdates] = useState<ServerUpdates>(), [updateError, setUpdateError] = useState<{cause: unknown; check: boolean}>(), [checking, setChecking] = useState(false);
  const [pausedUntil, setPausedUntil] = useState<string>(), [busy, setBusy] = useState(false), [problem, setProblem] = useState<string>();
  const [messageOpen, setMessageOpen] = useState(false), [restarting, setRestarting] = useState(false), [advanced, setAdvanced] = useState(false);
  const reread = useCallback(async () => {const next = await settings.readConfiguration(); if (lifetime.current()) setConfiguration(next);}, [settings, lifetime]);
  const load = useCallback(() => {
    void reread().then(() => {if (lifetime.current()) setLoadError(undefined);}, error => {if (lifetime.current()) setLoadError(error);});
    void readBackgroundPause(api).then(value => {if (lifetime.current()) setPausedUntil(value);}, () => {});
  }, [reread, api, lifetime]);
  useEffect(load, [load]);
  const loadUpdates = useCallback((check = false) => {
    if (!lifetime.current()) return;
    setChecking(true); setUpdateError(undefined);
    void (check ? checkServerUpdates : readServerUpdates)(api).then(next => {if (lifetime.current()) setUpdates(next);})
      .catch(error => {if (lifetime.current()) setUpdateError({cause: error, check});}).finally(() => {if (lifetime.current()) setChecking(false);});
  }, [api, lifetime]);
  useEffect(() => {loadUpdates();}, [loadUpdates]);

  const value = (id: string) => drafts.has(id) ? drafts.get(id) : settings.read(id)?.current;
  const enabled = (row: SpecRow) => {
    const rule = row.enableWhen; if (!rule) return true;
    const current = value(rule.id) as never;
    return (!rule.in || rule.in.includes(current)) && (!rule.notIn || !rule.notIn.includes(current));
  };
  const change = (row: SpecRow, next: unknown) => {if (!saving) {setSaveError(undefined); setDrafts(current => new Map(current).set(row.id, next));}};
  // Spec order, so Heavy work's own choice is stored before its window, and each save reads the
  // documents again: the next row is saved against the revision this one made.
  const save = async () => {
    if (saving || !lifetime.current()) return;
    setSaving(true); setSaveError(undefined);
    try {
      for (const row of [...main, ...extra].flatMap(group => group.rows)) {
        if (!drafts.has(row.id)) continue;
        await settings.save(row.id, drafts.get(row.id));
        await reread();
        if (lifetime.current()) setDrafts(current => {const next = new Map(current); next.delete(row.id); return next;});
      }
    } catch (error) {
      if (lifetime.current()) setSaveError(error);
      await reread().catch(() => {});
    } finally {if (lifetime.current()) setSaving(false);}
  };
  const paused = !!pausedUntil && Date.parse(pausedUntil) > serverNow();
  const pause = () => {
    setBusy(true); setProblem(undefined);
    void setBackgroundPause(api, !paused).then(next => {if (lifetime.current()) setPausedUntil(next);}, error => {if (lifetime.current()) setProblem(presentError(error, 'server-console').body);}).finally(() => {if (lifetime.current()) setBusy(false);});
  };
  const restart = () => {
    setBusy(true); setProblem(undefined);
    void restartServer(api).then(() => {if (lifetime.current()) {setRestarting(false); setBusy(false);}}, error => {if (lifetime.current()) {setBusy(false); setProblem(presentError(error, 'server-console').body);}});
  };

  const versionFacts = updates && [latestVersionLine(updates, t), updates.checkedAt ? when(Date.parse(updates.checkedAt)) : ''].filter(Boolean);
  const advancedSummary = settingsSummary(extra.flatMap(group => group.rows), row => {
    const fact = settings.read(row.id);
    return fact && {...fact, current: value(row.id)};
  }, t);
  return {
    t, main, extra, configuration, loadError, load, saving, saveError, save, hasDrafts: drafts.size > 0,
    discard: () => {setDrafts(new Map()); setSaveError(undefined);},
    updates, versionFacts, updateError, checking, loadUpdates, paused, busy, pause, problem,
    messageOpen, setMessageOpen, restarting, setRestarting, restart, openRestart: () => {setProblem(undefined); setRestarting(true);}, advanced, setAdvanced, advancedSummary,
    serverId: context.authority().serverId, value, change, enabled,
    hasSetting: (id: string) => settings.has(id) && settings.read(id) !== undefined,
    openCustom: (row: SpecRow) => {change(row, 'custom'); setAdvanced(true);},
  };
}

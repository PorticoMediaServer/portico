import {useCallback, useEffect, useRef, useState, useSyncExternalStore} from 'react';
import {artworkLimitText, openSubtitlesSummary, type MetadataSettings} from '../../../../../../packages/client-core/src/metadata-settings';
import {settingsRowIndex, settingsSpec, type SettingsRow} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import {NativeMetadataController, type MetadataPatch} from '../../../screens/settings-live/server/metadata-controller';
import {useFormState, useStorageForms, useStorageSet, type StorageForms} from '../../../screens/settings-live/server/storage/forms';
import {useSettingsRequestScope} from '../../../screens/settings-live/use-settings-request-scope';
import type {NativeShellRuntime} from '../../../shell/runtime';

const section = settingsSpec.find(value => value.scope === 'server' && value.id === 'metadata');
type CredentialKey = 'tmdbReadToken' | 'tvdbProjectKey' | 'openSubtitlesApiKey' | 'openSubtitlesUsername' | 'openSubtitlesPassword' | 'acoustidKey';
const credentialKeys: Readonly<Record<string, CredentialKey>> = {
  'metadata.tmdb': 'tmdbReadToken', 'metadata.tvdb': 'tvdbProjectKey', 'metadata.osKey': 'openSubtitlesApiKey',
  'metadata.osUser': 'openSubtitlesUsername', 'metadata.osPass': 'openSubtitlesPassword', 'metadata.acoustid': 'acoustidKey',
};
type CredentialEdit = {row: SettingsRow; original: MetadataSettings; key: CredentialKey; canRemove: boolean; removing?: boolean};

export function useMetadataController(ports: NativeSettingsLivePorts) {
  const scope = useSettingsRequestScope(ports.context);
  const current = useCallback(() => {
    const {account} = ports.context;
    return scope.lifetime.current() && account.canManage && account.account.role === 'owner'
      && !account.account.disabled && account.account.primaryProfileId === scope.authority.profileId;
  }, [scope, ports]);
  const [owned, setOwned] = useState<{controller: NativeMetadataController; scope: typeof scope}>();
  useEffect(() => {
    const controller = new NativeMetadataController(scope.api, current);
    setOwned({controller, scope});
    void controller.load();
    return () => controller.dispose();
  }, [scope, current]);
  return owned?.scope === scope ? {controller: owned.controller, current} : undefined;
}

export function useServerMetadata(controller: NativeMetadataController, current: () => boolean, runtime: NativeShellRuntime) {
  const {t} = runtime.text, state = useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot);
  const settings = state.data, advanced = settings?.advanced, disabled = state.busy || state.needsReload;
  const [editing, setEditing] = useState<CredentialEdit>(), editor = useRef<CredentialEdit | undefined>(undefined);
  const [text, setText] = useState(''), [confirmation, setConfirmation] = useState<object>(), refresh = useRef<object | undefined>(undefined);
  const row = (id: string) => settingsRowIndex.get(id)!.row;
  const groupTitle = (index: number) => {const key = section?.groups?.[index]?.title; return key ? t(key) : undefined;};
  const advancedTitle = (index: number) => {const key = section?.advanced?.groups?.[index]?.title; return key ? t(key) : undefined;};
  const options = (definition: SettingsRow, value: string) => {
    const offered = (definition.control.options ?? []).map(option => ({
      value: String(option.value), label: t(option.label), description: option.hint ? t(option.hint) : undefined,
    }));
    if (!offered.some(option => option.value === value)) offered.push({value, label: value, description: undefined});
    return offered;
  };
  const closeCredential = () => {
    if (!editing || editor.current !== editing || controller.getSnapshot().busy) return;
    editor.current = undefined; setEditing(undefined); setText('');
  };
  const credentials = (id: string, configured: boolean) => {
    const definition = row(id), key = credentialKeys[id]!;
    const status = key === 'openSubtitlesUsername' ? settings?.openSubtitlesUsername || t('server.metadata.noAccount')
      : configured ? t('server.metadata.keySaved')
        : key === 'tmdbReadToken' && settings?.tmdbCredentialUnreadable ? t('server.metadata.keyUnreadable')
          : key === 'tmdbReadToken' || key === 'tvdbProjectKey' ? t('server.metadata.keyOurs')
            : key === 'acoustidKey' && advanced?.acoustidKeySource === 'environment' ? t('server.metadata.acoustidServer')
              : t('server.metadata.keyNotSet');
    return {
      status,
      open: () => {
        if (!settings || !current() || disabled) return;
        const canRemove = configured || key === 'tmdbReadToken' && settings.tmdbCredentialUnreadable === true;
        const next = {row: definition, original: settings, key, canRemove};
        editor.current = next;
        setEditing(next);
        setText(key === 'openSubtitlesUsername' ? settings.openSubtitlesUsername : '');
      },
    };
  };
  const credentialReady = !!editing && (editing.removing ? editing.canRemove : !!text.trim());
  const openRemoval = () => {
    if (!editing?.canRemove || editor.current !== editing || !current() || disabled) return;
    const next = {...editing, removing: true};
    editor.current = next; setEditing(next); setText('');
  };
  const saveCredential = async () => {
    if (!editing || editor.current !== editing || !current() || !credentialReady || disabled) return;
    const didSave = await controller.save(editing.original, {[editing.key]: editing.removing ? '' : text});
    if (didSave && current() && editor.current === editing) closeCredential();
  };
  const closeRefresh = () => {
    if (!confirmation || refresh.current !== confirmation || controller.getSnapshot().busy) return;
    refresh.current = undefined; setConfirmation(undefined);
  };
  const refreshAll = async () => {
    if (!settings || !confirmation || refresh.current !== confirmation || !current()) return;
    const done = await controller.refreshAll(settings);
    if (done && current() && refresh.current === confirmation) closeRefresh();
  };
  return {
    state, settings, advanced, disabled, row, groupTitle, advancedTitle, options, credentials, editing, text,
    setText: (next: string) => {if (current() && editing && editor.current === editing) setText(next);},
    credentialReady, openRemoval, saveCredential, closeCredential, confirmation, closeRefresh, refreshAll,
    openRefresh: () => {if (current()) {const next = {}; refresh.current = next; setConfirmation(next);}},
    save: (patch: MetadataPatch) => {if (settings && current()) void controller.save(settings, patch);},
    reload: () => {void controller.load();},
    checkSubtitles: () => {if (settings && current()) void controller.checkOpenSubtitles(settings);},
    subtitles: settings ? openSubtitlesSummary(settings.openSubtitles, {...defaultI18n, t}) : undefined,
    summary: settings ? [
      settings.localArtworkLimitBytes === undefined ? '' : `${artworkLimitText(settings.localArtworkLimitBytes)} GB`,
      settings.artworkMaxEdge === undefined ? '' : `${settings.artworkMaxEdge} px`,
    ].filter(Boolean).join(' · ') : '',
  };
}

const datasetForms = ['titleData'] as const;
const datasetId = 'metadata.titleDataset';
const datasetLocation = settingsRowIndex.get(datasetId);
const datasetTitle = datasetLocation?.section.groups?.find(group => group.rows.some(row => row.id === datasetId))?.title;

export function useTitleDatasetForms(ports: NativeSettingsLivePorts, runtime: NativeShellRuntime) {
  return useStorageForms(ports, datasetForms, runtime.reportError);
}

export function useTitleDataset(group: StorageForms) {
  const form = group.forms.titleData, state = useFormState<Record<string, unknown>, unknown>(form), save = useStorageSet(group);
  return {
    row: datasetLocation?.row, title: datasetTitle, state, save, on: state.draft?.download !== false,
    change: (next: boolean) => {
      if (save.saving || form.getSnapshot().saving) return;
      form.set((draft: Record<string, unknown>) => ({...draft, download: next}));
      void group.set.save();
    },
    reload: () => {void form.load();},
    retry: () => {void group.set.save();},
  };
}

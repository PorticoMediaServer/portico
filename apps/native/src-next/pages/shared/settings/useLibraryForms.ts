import {useEffect, useMemo, useRef, useState} from 'react';
import {readOwnerKindMetadataAgents} from '../../../../../../packages/client-core/src/owner-library-requests';
import type {LibraryContentApi} from '../../../../../../packages/client-core/src/library-content';
import type {ManagedLibrary} from '../../../../../../packages/client-core/src/library-management';
import type {InventorySource, SourceSettings} from '../../../../../../packages/client-core/src/library-inventory';
import {localMetadataChoices, parseKindMetadataAgents, type KindMetadataAgents} from '../../../../../../packages/client-core/src/server-admin/metadata-agent';
import type {ServerRequester} from '../../../../../../packages/client-core/src/query/server-api';
import {emptyLibraryTrash, readLibraryTrash, removedAtNextScan, saveTrashRule, type LibraryTrash, type TrashRule} from '../../../../../../packages/client-core/src/admin/library-trash';
import type {ExpectedRemoteSource} from '../../../../../../packages/client-core/src/remote-source-selection';
import {defaultI18n} from '../../../../../../packages/i18n/src/index';
import type {LibraryFolderChoice} from '../../../screens/settings-live/owner-libraries/FolderSourcePicker';
import {ownerLibraryKind} from '../../../screens/settings-live/owner-libraries/presentation';

type Translate = typeof defaultI18n.t;
export type AddLibraryInput = Readonly<{name: string; kind: ManagedLibrary['kind']; folders: readonly string[]; expectedRemoteSource?: ExpectedRemoteSource; metadataAgent?: string; metadataLanguage?: string}>;

/** Add library (frame 17): its kind, a name that follows the kind until the owner types one, the folders chosen, and the
 * metadata source the server offers for that kind (Local only when it can't say), with that source's language. */
export function useAddLibrary(api: LibraryContentApi, error: unknown) {
  const t = defaultI18n.t;
  const [kind, setKind] = useState<ManagedLibrary['kind']>('movie'), [name, setName] = useState(defaultI18n.t('next.ownerLibraries.kind.movie')), [touched, setTouched] = useState(false);
  const [choice, setChoice] = useState<LibraryFolderChoice>({folders: []}), [exploring, setExploring] = useState(false);
  const folders = choice.folders;
  const [metadata, setMetadata] = useState<KindMetadataAgents>(), [metadataError, setMetadataError] = useState<unknown>();
  const [agent, setAgent] = useState(''), [language, setLanguage] = useState<string>();
  const chosenByOwner = useRef<string | undefined>(undefined);
  const agents = useMemo(() => metadata?.libraryKind === kind ? metadata.agents : [], [metadata, kind]);
  useEffect(() => {
    const request = new AbortController();
    let alive = true;
    setMetadata(undefined); setMetadataError(undefined);
    readOwnerKindMetadataAgents(api, kind, request.signal).then(raw => parseKindMetadataAgents(raw, kind)).then(
      value => {if (!alive) return; setMetadata(value); const chosen = value.agents.find(option => option.id === chosenByOwner.current)
          ?? value.agents.find(option => option.available !== false) ?? value.agents[0];
        if (chosen) {setAgent(chosen.id); setLanguage(chosen.defaultLanguage);}},
      error => {if (alive) {setMetadata(localMetadataChoices(kind)); setAgent('local'); setLanguage(undefined); setMetadataError(error);}});
    return () => {alive = false; request.abort();};
  }, [api, kind]);
  const chosenAgent = agents.find(option => option.id === agent);
  const valid = name.trim().length > 0 && folders.length > 0 && agents.some(option => option.id === agent);
  return {kind, name, choice, setChoice, folders, exploring, setExploring, agents, agent, chosenAgent, language, setLanguage, valid,
    formError: error ?? metadataError, errorOperation: (error !== undefined ? 'save' : 'load') as 'save' | 'load',
    chooseKind: (next: ManagedLibrary['kind']) => {setKind(next); if (!touched) setName(t(ownerLibraryKind(next).label));},
    typeName: (value: string) => {setName(value); setTouched(true);},
    chooseAgent: (option: Readonly<{id: string; defaultLanguage?: string}>) => {chosenByOwner.current = option.id; setAgent(option.id); setLanguage(option.defaultLanguage);},
    removeFolder: (path: string) => setChoice(current => ({...current, folders: current.folders.filter(value => value.path !== path),
      remoteFolders: current.remoteFolders?.filter(value => value.path !== path)})),
    input: (): AddLibraryInput => ({name: name.trim(), kind, folders: folders.map(folder => folder.path), metadataAgent: agent, ...(language ? {metadataLanguage: language} : {})})};
}

/** One folder's own form (frame 21a): path, name, where it lives, links and how often it is checked. */
export function useAddFolder(source: InventorySource | undefined, expectedRevision: number | undefined) {
  const [path, setPath] = useState(source?.path ?? ''), [name, setName] = useState(source?.name ?? ''),
    [classification, setClassification] = useState<'local' | 'network'>(source?.classification === 'network' ? 'network' : 'local'),
    [follow, setFollow] = useState(source?.followSymlinks ?? false), [interval, setInterval] = useState(source?.intervalSeconds ?? 3600),
    [exploring, setExploring] = useState(false);
  const [choice, setChoice] = useState<LibraryFolderChoice>({folders: source ? [{path: source.path, name: source.name || source.path}] : []});
  return {path, name, setName, classification, setClassification, follow, setFollow, interval, setInterval, exploring, setExploring, choice,
    valid: path.trim().length > 0 && expectedRevision !== undefined,
    use: (next: LibraryFolderChoice) => {const folder = next.folders[0]; if (folder) {setChoice(next); setPath(folder.path); if (!name) setName(folder.name); if (next.source) {setClassification('local'); setFollow(false);}}},
    input: (): SourceSettings => ({name: name.trim(), path: path.trim(), classification, followSymlinks: follow, intervalSeconds: interval, expectedRevision: expectedRevision ?? 0, acceptReplacement: false})};
}

export const TRASH_RULES = [['days', 'next.ownerLibraries.missingRuleDays'], ['scan', 'next.ownerLibraries.missingRuleScan'], ['manual', 'next.ownerLibraries.missingRuleManual']] as const;

/** A library's missing titles, its trash (03-scanning rules 59 to 62), as the web's LibraryTrash: the library's own rule
 * for emptying it, the list with when each went missing and when it goes, and Empty trash once the count is typed. A rule
 * that would remove more at the next scan says how many first (discourage, don't block). */
export function useLibraryTrash({api, libraryId, parentCurrent, t}: {api: ServerRequester; libraryId: string; parentCurrent: () => boolean; t: Translate}) {
  const [trash, setTrash] = useState<LibraryTrash>(), [failed, setFailed] = useState(false), [busy, setBusy] = useState(false), [days, setDays] = useState('');
  const [listOpen, setListOpen] = useState(false), [confirming, setConfirming] = useState(false), [typed, setTyped] = useState(''), [removed, setRemoved] = useState<number>();
  const [asking, setAsking] = useState<Readonly<{rule: TrashRule; count: number}>>();
  const lifetime = useRef<AbortController | undefined>(undefined);
  const live = (capture: AbortController | undefined): capture is AbortController => !!capture && lifetime.current === capture && !capture.signal.aborted && parentCurrent();
  const load = async (capture: AbortController) => {
    try {const next = await readLibraryTrash(api, libraryId, capture.signal); if (live(capture)) {setTrash(next); setDays(String(next.rule.days)); setFailed(false);}}
    catch {if (live(capture)) setFailed(true);}
  };
  const ports = useRef({load});
  useEffect(() => {ports.current = {load};});
  useEffect(() => {
    const capture = new AbortController(); lifetime.current = capture; setTrash(undefined); void ports.current.load(capture);
    return () => {capture.abort(); if (lifetime.current === capture) lifetime.current = undefined;};
  }, [api, libraryId]);
  const run = async (work: (capture: AbortController) => Promise<void>) => {
    const capture = lifetime.current;
    if (!live(capture) || busy) return;
    setBusy(true); setFailed(false);
    try {await work(capture);} catch {if (live(capture)) setFailed(true);} finally {if (live(capture)) setBusy(false);}
  };
  const save = (rule: TrashRule) => {setAsking(undefined); void run(async capture => {await saveTrashRule(api, libraryId, rule, capture.signal); if (live(capture)) await load(capture);});};
  const choose = (rule: TrashRule) => {
    if (!trash || busy) return;
    const count = removedAtNextScan(trash, rule);
    if (count > removedAtNextScan(trash, trash.rule)) setAsking({rule, count}); else save(rule);
  };
  const empty = () => void run(async capture => {
    const count = await emptyLibraryTrash(api, libraryId, typed.trim(), capture.signal);
    if (!live(capture)) return;
    setConfirming(false); setTyped(''); setRemoved(count); await load(capture);
  });
  const dayCount = Number(days), daysValid = /^\d+$/.test(days) && dayCount >= 1 && dayCount <= 3650;
  const removal = (removedAt?: string) => !trash || trash.rule.mode === 'manual' || !removedAt ? undefined
    : trash.rule.mode === 'scan' ? t('next.ownerLibraries.missingRemovedNextScan') : t('next.ownerLibraries.missingRemovedOn', {date: defaultI18n.date(removedAt)});
  return {trash, failed, busy, days, setDays, dayCount, daysValid, listOpen, setListOpen, confirming, setConfirming, typed, setTyped, removed, asking, setAsking,
    choose, save, empty, openList: () => {if (parentCurrent()) {setRemoved(undefined); setListOpen(true);}}, startEmpty: () => {setTyped(''); setConfirming(true);},
    items: (trash?.items ?? []).map(item => ({key: item.id, title: item.year ? t('next.ownerLibraries.missingTitleYear', {title: item.title, year: String(item.year)}) : item.title,
      lines: [t('next.ownerLibraries.missingSince', {date: defaultI18n.date(item.missingSince)}), ...(removal(item.removedAt) ? [removal(item.removedAt)!] : [])]})),
    label: (key: string) => t(`next.settingsSpec.server.libraries.panels.library.tabs.3.groups.2.rows.${key}` as Parameters<Translate>[0])};
}

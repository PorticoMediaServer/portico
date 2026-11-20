import {readOwnerKindMetadataAgents} from '../../../../../../packages/client-core/src/owner-library-requests';
import {useEffect, useMemo, useRef, useState} from 'react';
import type {LibraryContentApi} from '../../../../../../packages/client-core/src/library-content';
import type {ManagedLibrary} from '../../../../../../packages/client-core/src/library-management';
import {localMetadataChoices, parseKindMetadataAgents, type KindMetadataAgents} from '../../../../../../packages/client-core/src/server-admin/metadata-agent';
import {defaultI18n} from '../../../../../../packages/i18n/src/index';
import type {ExpectedRemoteSource} from '../../../../../../packages/client-core/src/remote-source-selection';
import {ownerLibraryKind} from '../../../screens/settings-live/owner-libraries/presentation';
import type {LibraryFolderChoice} from './useFolderSourcePicker';

export type AddLibraryInput = Readonly<{name: string; kind: ManagedLibrary['kind']; folders: readonly string[]; expectedRemoteSource?: ExpectedRemoteSource; metadataAgent?: string; metadataLanguage?: string}>;

export function useAddOwnerLibrary(api: LibraryContentApi, onCreate: (input: AddLibraryInput) => void) {
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
  const chooseKind = (next: ManagedLibrary['kind']) => {setKind(next); if (!touched) setName(t(ownerLibraryKind(next).label));};
  const valid = name.trim().length > 0 && folders.length > 0 && agents.some(option => option.id === agent);
  const chooseAgent = (option: KindMetadataAgents['agents'][number]) => {
    chosenByOwner.current = option.id;
    setAgent(option.id);
    setLanguage(option.defaultLanguage);
  };
  const remove = (path: string) => setChoice(current => ({...current,
    folders: current.folders.filter(folder => folder.path !== path),
    remoteFolders: current.remoteFolders?.filter(folder => folder.path !== path),
  }));
  const enterName = (value: string) => {setName(value); setTouched(true);};
  const save = () => onCreate({name: name.trim(), kind, folders: folders.map(folder => folder.path), metadataAgent: agent,
    ...(language ? {metadataLanguage: language} : {})});

  return {t, kind, name, enterName, chooseKind, choice, setChoice, folders, remove, exploring, setExploring, agents,
    chosenAgent, agent, chooseAgent, language, setLanguage, valid, metadataError, save};
}

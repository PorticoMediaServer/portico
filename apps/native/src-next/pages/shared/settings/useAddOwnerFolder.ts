import {useState} from 'react';
import type {InventorySource, SourceSettings} from '../../../../../../packages/client-core/src/library-inventory';
import type {LibraryFolderChoice} from './useFolderSourcePicker';

export function useAddOwnerFolder(source: InventorySource | undefined, expectedRevision: number | undefined, onSave: (input: SourceSettings) => void) {
  const [path, setPath] = useState(source?.path ?? '');
  const [name, setName] = useState(source?.name ?? '');
  const [classification, setClassification] = useState<'local' | 'network'>(source?.classification === 'network' ? 'network' : 'local');
  const [follow, setFollow] = useState(source?.followSymlinks ?? false);
  const [interval, setInterval] = useState(source?.intervalSeconds ?? 3600);
  const [exploring, setExploring] = useState(false);
  const [choice, setChoice] = useState<LibraryFolderChoice>({folders: source ? [{path: source.path, name: source.name || source.path}] : []});
  const valid = path.trim().length > 0 && expectedRevision !== undefined;
  const choose = (next: LibraryFolderChoice) => {
    const folder = next.folders[0];
    if (!folder) return;
    setChoice(next);
    setPath(folder.path);
    if (!name) setName(folder.name);
    if (next.source) {setClassification('local'); setFollow(false);}
  };
  const save = () => {
    if (valid && expectedRevision !== undefined) onSave({
      name: name.trim(), path: path.trim(), classification, followSymlinks: follow,
      intervalSeconds: interval, expectedRevision, acceptReplacement: false,
    });
  };
  return {path, name, setName, classification, setClassification, follow, setFollow, interval, setInterval, exploring, setExploring, choice, choose, valid, save};
}

import {useState} from 'react';
import {settingsBinding, readSettingsBinding, settingsBindingPatch} from '../../../../../../packages/client-core/src/presentation/settings-bindings';
import type {BackupSettings} from '../../../../../../packages/client-core/src/server-admin/server-backups';

/** One draft for the real days/hour/keep fields, saved through the page's existing backup source. */
export function useBackupSchedule(settings: BackupSettings, saving: boolean) {
  const [draft, setDraft] = useState(settings), [chosen, setChosen] = useState(false), [valid, setValid] = useState(true);
  const sources = {forms: {backups: draft}};
  const read = (id: string) => readSettingsBinding(settingsBinding(id)!, sources, id)?.current;
  const mode = chosen ? 'custom' : read('storage.backupSchedule');
  const change = (id: string, value: unknown) => {
   if (saving) return;
   const patch = settingsBindingPatch(settingsBinding(id)!, value, sources);
   setDraft(current => ({...current, ...patch.values}));
   if (id === 'storage.backupSchedule') setChosen(value === 'custom');
  };
  const dirty = JSON.stringify([draft.days, draft.hour, draft.keep]) !== JSON.stringify([settings.days, settings.hour, settings.keep]);
  return {mode, read, change, dirty, valid, setValid, values: {days: draft.days, hour: draft.hour, keep: draft.keep},
   discard: () => {setDraft(settings); setChosen(false); setValid(true);}};
}

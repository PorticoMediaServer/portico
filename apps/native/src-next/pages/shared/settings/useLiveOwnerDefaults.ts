import {useEffect, useRef, useState} from 'react';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeLiveOwnerController} from '../../../screens/settings-live/live-owner-controller';
import type {LiveDefaults} from '../../../screens/settings-live/live-owner-model';

export function useLiveOwnerDefaults(controller: NativeLiveOwnerController, runtime: NativeShellRuntime) {
  const {t} = runtime.text;
  const [advanced, setAdvanced] = useState(false), [custom, setCustom] = useState(false);
  const [document, setDocument] = useState<Readonly<{revision: number; settings: LiveDefaults}>>(), [draft, setDraft] = useState<LiveDefaults>(), [error, setError] = useState<unknown>(), [busy, setBusy] = useState(false), [saved, setSaved] = useState(false);
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    let active = true;
    setError(undefined);
    void controller.liveDefaults().then(value => {if (active) {setDocument(value); setDraft(value.settings);}}, value => {if (active) setError(value);});
    return () => {active = false;};
  }, [controller, attempt]);
  const dirty = !!document && !!draft && JSON.stringify(draft) !== JSON.stringify(document.settings);
  const patch = (value: Partial<LiveDefaults>) => {setSaved(false); setDraft(current => current ? {...current, ...value} : current);};
  const pending = useRef(false);
  const save = async () => {
    if (!document || !draft || pending.current) return;
    pending.current = true;
    setBusy(true); setError(undefined);
    try {const next = await controller.saveLiveDefaults(document.revision, draft); setDocument(next); setDraft(next.settings); setSaved(true);}
    catch (failure) {setError(failure);}
    finally {pending.current = false; setBusy(false);}
  };
  const guideDayChoices = [...new Set([3, 7, 14, ...(draft && draft.guideDays > 0 ? [draft.guideDays] : [])])].sort((left, right) => left - right);
  const retry = () => {
    if (document) void save();
    else setAttempt(value => value + 1);
  };
  return {t, advanced, setAdvanced, custom, setCustom, document, draft, error, busy, saved, dirty, patch, save, retry, guideDayChoices};
}

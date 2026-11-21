import {useLayoutEffect, useState, useSyncExternalStore} from 'react';
import {applySettingsBindingPatch, peopleStreamLimits, readSettingsBinding, settingsBinding, settingsBindingPatch} from '../../../../../../packages/client-core/src/presentation/settings-bindings';
import {settingsSpec} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import type {AccessMember, MemberLimits} from '../../../../../../packages/client-core/src/server-administration';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import type {InvitationOpening, NativePeopleSource} from '../../../screens/settings-live/server/people-source';
import {useFormState, useStorageForms, useStorageSet, type StorageForms} from '../../../screens/settings-live/server/storage/forms';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {Confirmation} from './useServerPeople';

const tabs = ['account', 'access', 'profiles', 'devices', 'advanced'];
const streamChoices = ['default', 'unlimited', '1', '2', '3', '4', '6'];

export function useServerPersonDetails(source: NativePeopleSource, person: AccessMember, ask: (value: Confirmation) => void) {
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
  const [tab, setTab] = useState('account'), [notes, setNotes] = useState(person.notes ?? '');
  const [limits, setLimits] = useState<MemberLimits>(person.limits);
  const [allLibraries, setAllLibraries] = useState(!person.allowedLibraries.length);
  const [libraries, setLibraries] = useState<readonly string[]>(person.allowedLibraries);
  const streamLimit = peopleStreamLimits.read({limits});
  const managed = source.ownerControls() && person.role !== 'owner' && person.accountId !== source.context.account.account.id;
  const current = () => source.current() && source.getSnapshot().person === person && !source.getSnapshot().busy;
  const selectTab = (value: string) => {
    if (!current() || !tabs.includes(value)) return;
    setTab(value);
    if (value === 'devices') void source.loadDevices(person);
    if (value === 'access') void source.loadLibraries();
  };
  const reviewDeletion = () => {
    if (!current()) return;
    void source.previewDeletion(person).then(preview => {if (preview && current()) ask({person, action: 'remove', preview});});
  };
  return {
    state, tab, tabs, selectTab, notes, setNotes, managed, libraries, allLibraries, limits, streamLimit, streamChoices,
    saveNotes: () => {if (current()) void source.notes(person, notes);},
    changeRole: (role: string) => {if (current() && role !== person.role && (role === 'admin' || role === 'member')) ask({person, action: 'role', role});},
    changeEnabled: () => {if (current()) ask({person, action: person.disabled ? 'enable' : 'disable'});},
    reviewDeletion,
    changeLibraries: (all: boolean, ids: readonly string[]) => {if (current()) {setAllLibraries(all); setLibraries(ids);}},
    saveLibraries: () => {if (current() && (allLibraries || libraries.length)) void source.access(person, allLibraries ? [] : libraries);},
    changeRemote: (allowRemote: boolean) => {if (current() && managed) setLimits({...limits, allowRemote});},
    changeStreamLimit: (value: string) => {if (current() && managed) setLimits(peopleStreamLimits.patch({limits}, value));},
    changeTokens: (accessTokens: boolean) => {if (current() && managed) setLimits({...limits, accessTokens});},
    saveLimits: () => {if (current()) void source.limits(person, limits);},
    reloadDevices: () => {if (current()) void source.loadDevices(person);},
    moreDevices: () => {if (current()) void source.loadDevices(person, true);},
    signOutDevice: (device?: NonNullable<typeof state.devices>['items'][number]) => {if (current()) ask({person, device, action: 'devices'});},
  };
}

export function usePeopleLibraryAccess(source: NativePeopleSource, selected: readonly string[]) {
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
  return {
    state, rows: [...(state.libraries ?? []), ...selected.filter(id => !state.libraries?.some(library => library.id === id)).map(id => ({id, name: id}))],
    retry: () => {void source.loadLibraries();},
  };
}

export function useServerInvitationEditor(source: NativePeopleSource, opening: InvitationOpening) {
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
  const [notes, setNotes] = useState(opening.invitation?.notes ?? '');
  const [role, setRole] = useState(opening.invitation?.role === 'admin' ? 'admin' : 'member'), [days, setDays] = useState('7');
  const [all, setAll] = useState(!opening.invitation?.allowedLibraries.length);
  const [libraries, setLibraries] = useState<readonly string[]>(opening.invitation?.allowedLibraries ?? []);
  useLayoutEffect(() => {void source.loadLibraries();}, [source]);
  const current = () => source.current() && source.getSnapshot().invitationOpening === opening && !source.getSnapshot().busy;
  return {
    state, notes, setNotes, role, days, all, libraries, close: () => {if (current()) source.closeInvitation();},
    changeRole: (value: string) => {if (current() && (value === 'admin' || value === 'member')) setRole(value);},
    changeDays: (value: string) => {if (current() && ['1', '3', '7', '30'].includes(value)) setDays(value);},
    changeLibraries: (next: boolean, ids: readonly string[]) => {if (current()) {setAll(next); setLibraries(ids);}},
    save: () => {
      if (current() && !state.loading && (all || libraries.length)) {
        void source.saveInvitation(opening, {notes, role: role === 'admin' ? 'admin' : 'member', days: Number(days), allowedLibraries: all ? [] : libraries});
      }
    },
    share: () => {if (current()) source.shareInvitation(opening);},
  };
}

const ids = ['streamLimits'] as const;
const advancedRows = settingsSpec.find(section => section.scope === 'server' && section.id === 'people')?.advanced?.groups?.flatMap(group => group.rows) ?? [];

export function usePeopleAdvancedForms(ports: NativeSettingsLivePorts, runtime: NativeShellRuntime) {
  return useStorageForms(ports, ids, runtime.reportError);
}

export function usePeopleAdvanced(group: StorageForms) {
  const form = group.forms.streamLimits, state = useFormState<Record<string, unknown>, unknown>(form), save = useStorageSet(group);
  const rows = state.draft ? advancedRows.flatMap(row => {
    const binding = settingsBinding(row.id);
    if (!binding || binding.form !== 'streamLimits') return [];
    const value = readSettingsBinding(binding, {forms: {streamLimits: state.draft}});
    return value ? [{
      row, value: value.current,
      onChange: (next: unknown) => {
        const patch = settingsBindingPatch(binding, next);
        form.set(current => applySettingsBindingPatch(current, patch));
      },
    }] : [];
  }) : [];
  return {
    state, save, rows, load: () => {void form.load();},
    discard: () => group.set.discard(), submit: () => {void group.set.save();},
  };
}

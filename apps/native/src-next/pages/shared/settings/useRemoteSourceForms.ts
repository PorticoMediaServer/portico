import {useEffect, useLayoutEffect, useRef, useState} from 'react';
import {Linking} from 'react-native';
import type {defaultI18n} from '../../../../../../packages/i18n/src';
import {defaultRemotePreset, missingField, remotePresets, remoteSourceCreate, type RemoteFolder, type RemoteLimits, type RemotePreset, type RemoteSource} from '../../../../../../packages/client-core/src/remote-storage';
import type {AdminLibrary} from '../../../../../../packages/contracts/src/next/api';
import type {NativeRemoteSourcesController} from '../../../screens/settings/remote-sources/controller';
import {remoteSourceError, type ChosenSourceFolder} from '../../../screens/settings/remote-sources/messages';

type Translate = typeof defaultI18n.t;
type Form = {controller: NativeRemoteSourcesController; t: Translate; isCurrent: () => boolean};

type Kind = 'webdav' | 'rclone' | 'cloud' | 'newRclone' | 'drive';
/** Runs one action, showing its refusal; a second tap while it runs does nothing. */
function useRun(t: Translate, isCurrent: () => boolean) {
  const [busy, setBusy] = useState(false), [error, setError] = useState<string>();
  const active = useRef(false), flight = useRef(false);
  useLayoutEffect(() => {
    active.current = true;
    return () => {active.current = false;};
  }, []);
  const current = () => active.current && isCurrent();
  const run = (work: () => Promise<void>) => {
    if (flight.current || !current()) return;
    flight.current = true;
    setBusy(true); setError(undefined);
    void work().catch(failure => {if (current()) setError(remoteSourceError(t, failure));}).finally(() => {flight.current = false; if (current()) setBusy(false);});
  };
  return {busy, error, run, current};
}

export function useAddRemoteSource({controller, t, isCurrent, onSaved}: Form & {onSaved: (source: RemoteSource) => void}) {
  const {busy, error, run, current} = useRun(t, isCurrent);
  const [step, setStep] = useState<1 | 2 | 3>(1), [kind, setKind] = useState<Kind>('webdav'), [created, setCreated] = useState<RemoteSource>();
  const [f, setF] = useState({name: '', address: '', user: '', password: '', config: '', path: '', token: '', provider: 'onedrive' as 'onedrive' | 'dropbox', remoteType: '', options: '',
    driveMode: 'key' as 'key' | 'oauth', key: '', clientId: '', clientSecret: '', signIn: '', redirectedUrl: ''});
  const [preset, setPreset] = useState<RemotePreset>(defaultRemotePreset), [limits, setLimits] = useState<RemoteLimits>(remotePresets[1]!.limits);
  const set = (patch: Partial<typeof f>) => setF(value => ({...value, ...patch}));
  const testAndSave = () => run(async () => {
    let source: RemoteSource;
    if (kind === 'cloud') source = await controller.addCloudToken({name: f.name.trim(), provider: f.provider, token: f.token, path: f.path, preset});
    else if (kind === 'newRclone') source = await controller.addRcloneFromFields({name: f.name.trim(), type: f.remoteType.trim(), path: f.path, preset,
      options: Object.fromEntries(f.options.split('\n').map(line => line.split('=')).filter(parts => parts.length >= 2).map(([key, ...rest]) => [key!.trim(), rest.join('=').trim()]))});
    else {
      const body = remoteSourceCreate(kind === 'webdav' ? {kind, name: f.name, address: f.address, user: f.user, password: f.password}
        : kind === 'rclone' ? {kind, name: f.name, config: f.config, path: f.path}
          : f.driveMode === 'key' ? {kind: 'drive', name: f.name, path: f.path, serviceAccountKey: f.key} : {kind: 'drive', name: f.name, path: f.path, signIn: f.signIn, redirectedUrl: f.redirectedUrl}, preset);
      if ('missing' in body) throw missingField(body.missing);
      source = await controller.add(body);
    }
    if (current()) {setCreated(source); setStep(3);}
  });
  const saveReading = () => run(async () => {
    if (!created) return;
    const source = await controller.change(created.id, {preset, limits});
    if (current()) onSaved(source);
  });
  const signIn = () => run(async () => {
    const started = await controller.startDriveSignIn(f.clientId, f.clientSecret);
    if (!current()) return;
    set({signIn: started.signIn});
    await Linking.openURL(started.address);
  });
  return {busy, error, step, setStep, kind, setKind, f, set, preset, setPreset, limits, setLimits, testAndSave, saveReading, signIn};
}

export function useRemoteFolderStep({controller, t, source, isCurrent, onDone}: Form & {source: RemoteSource; onDone: () => void}) {
  const {busy, error, run, current} = useRun(t, isCurrent);
  const [path, setPath] = useState(''), [folders, setFolders] = useState<readonly RemoteFolder[]>(), [libraries, setLibraries] = useState<readonly AdminLibrary[]>([]);
  const [target, setTarget] = useState(''), [name, setName] = useState(source.name), [kind, setKind] = useState('movies');
  // isCurrent is read when an answer arrives; a new one each render must not read again.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => {setFolders(undefined); void controller.folders(source.id, path).then(list => {if (current()) setFolders(list);}, () => {if (current()) setFolders([]);});}, [controller, source.id, path]);
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => {void controller.libraries().then(list => {if (current()) setLibraries(list);}, () => {});}, [controller]);
  const save = () => run(async () => {
    await controller.putFolderInLibrary(source, path, target ? {id: target} : {name: name.trim(), kind});
    if (current()) onDone();
  });
  return {busy, error, path, setPath, folders, libraries, target, setTarget, name, setName, kind, setKind, save};
}

export function useRemoteSourceEditor({controller, t, source, isCurrent, onClose}: Form & {source: RemoteSource; onClose: () => void}) {
  const {busy, error, run, current} = useRun(t, isCurrent);
  const [shown, setShown] = useState(source), [name, setName] = useState(source.name), [secret, setSecret] = useState(''), [confirm, setConfirm] = useState<string>();
  const startPreset = remotePresets.find(one => one.id === source.preset) ?? remotePresets.find(one => one.id === defaultRemotePreset)!;
  // A source saved before its preset step holds no limits yet: they start as its preset's own.
  const [preset, setPreset] = useState<RemotePreset>(startPreset.id), [limits, setLimits] = useState(source.limits.connections > 0 && source.limits.perSecond > 0 ? source.limits : startPreset.limits);
  const save = () => run(async () => {
    const replaced = !secret ? {} : source.kind === 'webdav' ? {webdav: {address: shown.webdav!.address, ...(shown.webdav?.user ? {user: shown.webdav.user} : {}), password: secret}}
      : source.kind === 'rclone' ? {rclone: {config: secret}} : {drive: {serviceAccountKey: secret}};
    const next = await controller.change(source.id, {name: name.trim(), preset, limits, ...replaced});
    if (current()) {setShown(next); setSecret('');}
  });
  const test = () => run(async () => {const tested = await controller.test(source.id); if (current()) setShown(tested);});
  const disconnect = () => run(async () => {await controller.remove(source.id); if (current()) onClose();});
  return {busy, error, shown, name, setName, secret, setSecret, confirm, setConfirm, preset, setPreset, limits, setLimits, save, test, disconnect};
}

export function useSourceFolderBrowser({controller, source, t, isCurrent, multiple}: Form & {source: RemoteSource; multiple: boolean}) {
  const [path, setPath] = useState('');
  const [folders, setFolders] = useState<readonly RemoteFolder[]>();
  const [chosen, setChosen] = useState<readonly ChosenSourceFolder[]>([]);
  const [error, setError] = useState<string>();
  const ports = useRef({t, isCurrent});
  useLayoutEffect(() => {ports.current = {t, isCurrent};}, [t, isCurrent]);
  useEffect(() => {
    let active = true;
    const current = () => active && ports.current.isCurrent();
    setFolders(undefined);
    setError(undefined);
    void controller.folders(source.id, path).then(
      value => {if (current()) setFolders(value);},
      failure => {if (current()) setError(remoteSourceError(ports.current.t, failure));},
    );
    return () => {active = false;};
  }, [controller, source.id, path]);
  const here = {name: path.split('/').pop() || source.name, path};
  const picked = chosen.some(folder => folder.path === path);
  const toggle = () => {
    if (isCurrent()) setChosen(items => picked ? items.filter(folder => folder.path !== path) : multiple ? [...items, here] : [here]);
  };
  return {path, setPath, folders, chosen, error, here, picked, toggle};
}

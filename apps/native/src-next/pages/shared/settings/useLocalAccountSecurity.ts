import {useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {IdentityClient, type TwoFactorEnrolment} from '../../../../../../packages/client-core/src/identity-client';
import type {Device} from '../../../../../../packages/client-core/src/identity-devices';
import type {TwoFactorState} from '../../../../../../packages/client-core/src/identity-restrictions';
import {passwordStrength} from '../../../../../../packages/client-core/src/password-strength';
import {nativeAccounts} from '../../../platform/accounts';
import {copyText} from '../../../platform/clipboard';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeSettingsPanelDeps} from '../../../screens/settings-live/settings-panels';
import {createNativeAccountSecurityScope, nativeAccountSecurityAllowed, type NativeAccountSecurityKind, type NativeAccountSecurityScope} from '../../../screens/settings-live/native-account-security';

export type LocalAccountSecurityProps = NativeSettingsPanelDeps & {kind: NativeAccountSecurityKind; runtime: NativeShellRuntime};

type Opening = Readonly<{scope: NativeAccountSecurityScope; client: IdentityClient}>;
type Mode = 'main' | 'disable' | 'confirm-disable' | 'replace' | 'confirm-remove' | 'confirm-all';

export function useLocalAccountSecurity({kind, context, lifetime, t, runtime}: LocalAccountSecurityProps) {
  const navigation = useSyncExternalStore(context.navigation.subscribe, context.navigation.getSnapshot);
  const [opening, setOpening] = useState<Opening>(), openingRef = useRef<Opening | undefined>(undefined);
  const mounted = useRef(false), busyRef = useRef(false), copyRef = useRef(false), shownCodes = useRef<readonly string[] | undefined>(undefined);
  const stageRef = useRef<readonly unknown[]>([]);
  const [busy, setBusy] = useState(false), [error, setError] = useState<unknown>(), [notice, setNotice] = useState('');
  const [factor, setFactor] = useState<TwoFactorState>(), [enrolment, setEnrolment] = useState<TwoFactorEnrolment>(), [codes, setCodes] = useState<readonly string[]>();
  const [password, setPassword] = useState(''), [nextPassword, setNextPassword] = useState(''), [confirmation, setConfirmation] = useState(''), [code, setCode] = useState('');
  const [devices, setDevices] = useState<readonly Device[]>(), [device, setDevice] = useState<Device>(), [deviceName, setDeviceName] = useState('');
  const [mode, setMode] = useState<Mode>('main'), [copied, setCopied] = useState(false);
  const clear = () => {
    shownCodes.current = undefined;
    setError(undefined); setFactor(undefined); setEnrolment(undefined); setCodes(undefined); setPassword(''); setNextPassword(''); setConfirmation(''); setCode('');
    setDevices(undefined); setDevice(undefined); setDeviceName(''); setMode('main'); setCopied(false); setBusy(false);
  };
  const close = () => {openingRef.current?.scope.dispose(); openingRef.current = undefined; busyRef.current = false; copyRef.current = false; setOpening(undefined); clear();};
  useLayoutEffect(() => {
    mounted.current = true; clear(); setNotice(''); setOpening(undefined);
    return () => {mounted.current = false; openingRef.current?.scope.dispose(); openingRef.current = undefined; busyRef.current = false; copyRef.current = false; shownCodes.current = undefined;};
  }, [context, lifetime, kind, navigation.revision, navigation.route]); // Clear plaintext on a different Settings opening.
  const current = (held: Opening | undefined): held is Opening => !!held && mounted.current && openingRef.current === held && !runtime.retirementSignal?.aborted && held.scope.current()
    && (kind === 'devices' || !context.account.account.hostedAccountId);
  const run = async <T,>(held: Opening | undefined, work: (client: IdentityClient, scope: NativeAccountSecurityScope) => Promise<T>, accepted: (value: T) => void) => {
    if (!current(held) || busyRef.current) return;
    busyRef.current = true; setBusy(true); setError(undefined);
    try {const value = await work(held.client, held.scope); if (current(held)) accepted(value);}
    catch (failure) {if (current(held)) setError(failure);}
    finally {if (current(held)) {busyRef.current = false; setBusy(false);}}
  };
  const load = (held: Opening) => {
    if (kind === 'devices') void run(held, (client, source) => client.devices(source.installationId ?? '', source.signal), setDevices);
    else void run(held, (client, source) => client.twoFactor(source.signal), setFactor);
  };
  const open = () => {
    if (kind !== 'devices' && context.account.account.hostedAccountId) return;
    if (openingRef.current || !mounted.current || !lifetime.current() || !context.current() || !nativeAccountSecurityAllowed(context, kind)) return;
    const now = context.navigation.getSnapshot();
    if (now.revision !== navigation.revision || now.route !== navigation.route || now.route.kind !== 'shell' || now.route.destination.kind !== 'settings') return;
    close(); setNotice('');
    let scope: NativeAccountSecurityScope;
    try {scope = createNativeAccountSecurityScope(context, lifetime, kind);} catch (failure) {if ((failure as {name?: string})?.name === 'AbortError') return; throw failure;}
    const held = {scope, client: new IdentityClient(scope)};
    openingRef.current = held; setOpening(held); load(held);
  };
  const held = opening;
  const stage = [mode, factor, enrolment, codes, device, password, nextPassword, confirmation, code, deviceName] as const;
  stageRef.current = stage;
  const stageCurrent = () => current(held) && stage.every((value, index) => stageRef.current[index] === value);
  const perform = <T,>(work: (client: IdentityClient, scope: NativeAccountSecurityScope) => Promise<T>, accepted: (value: T) => void) => {if (stageCurrent()) return run(held, work, accepted);};
  const revealCodes = (value: readonly string[]) => {shownCodes.current = value; setCodes(value);};
  const edit = (setter: (value: string) => void) => (value: string) => {if (stageCurrent() && !busyRef.current) setter(value);};
  const go = (next: Mode) => {if (stageCurrent() && !busyRef.current) {setMode(next); setPassword(''); setCode(''); setError(undefined);}};
  const saved = (message: string) => {close(); setNotice(message);};
  const signOutHere = async (scope: NativeAccountSecurityScope) => {
    if (!scope.current()) return;
    await nativeAccounts.signOutServer(scope.serverId, scope.familyId);
  };
  const refreshDevices = async (client: IdentityClient, scope: NativeAccountSecurityScope) => client.devices(scope.installationId ?? '', scope.signal);
  const copyCodes = async () => {
    if (!stageCurrent() || !codes?.length || copyRef.current) return;
    copyRef.current = true;
    try {await copyText(codes.join('\n')); if (current(held)) {setCopied(true); setError(undefined);}}
    catch (failure) {if (current(held)) setError(failure);}
    finally {if (openingRef.current === held) copyRef.current = false;}
  };
  const strength = passwordStrength(nextPassword), matching = !confirmation || confirmation === nextPassword;
  const canSave = !!(password && strength.acceptable && confirmation && matching && (!factor?.enabled || code.trim()));
  const verifiedCode = code.replace(/\s/g, '');
  const canVerify = /^\d{6}$/.test(verifiedCode);
  const savePassword = () => {
    if (canSave) void perform(client => client.changePassword(password, nextPassword, factor?.enabled ? code.trim() : undefined), () => saved(t('web.reset.doneTitle')));
  };
  const submitFactor = () => {
    if (!stageCurrent() || busyRef.current || !password || !code.trim()) return;
    if (mode === 'disable') {setMode('confirm-disable'); return;}
    void perform((client, scope) => client.replaceRecoveryCodes(password, code.trim(), scope.signal), value => {
      revealCodes(value); setPassword(''); setCode(''); setMode('main');
    });
  };
  const verifyFactor = () => {
    if (!canVerify) return;
    void perform(async client => {
      const value = await client.verifyTwoFactor(password, verifiedCode);
      if (!value.enabled) throw new Error('invalid_two_factor_state');
      return value;
    }, value => {
      setFactor(value); revealCodes(value.recoveryCodes); setEnrolment(undefined); setPassword(''); setCode('');
    });
  };
  const enrol = () => {
    if (password) void perform(client => client.enrolTwoFactor(password), value => {setEnrolment(value); setCode('');});
  };
  const disable = () => {
    void perform(client => client.disableTwoFactor(password, code.trim()), () => saved('Two-step verification turned off.'));
  };
  const signOutAll = () => {
    void perform(async (client, scope) => {await client.signOutEverywhere(); await signOutHere(scope);}, () => close());
  };
  const pickDevice = (row: Device) => {
    if (stageCurrent() && !busyRef.current) {setDevice(row); setDeviceName(row.name); setMode('main'); setError(undefined);}
  };
  const removeDevice = () => {
    if (!device || device.current) return;
    void perform(async (client, scope) => {await client.removeDevice(device.id); return refreshDevices(client, scope);},
      value => {setDevices(value); setDevice(undefined); setMode('main');});
  };
  const signOutDevice = () => {
    if (!device) return;
    void perform(async (client, scope) => {
      await client.signOutDevice(device.id);
      if (device.current) {await signOutHere(scope); return undefined;}
      return refreshDevices(client, scope);
    }, value => {if (value) {setDevices(value); setDevice(undefined);} else close();});
  };
  const renameDevice = () => {
    const name = deviceName.trim();
    if (!device || !name || name === device.name) return;
    void perform(async (client, scope) => {await client.editDevice(device.id, {name}); return refreshDevices(client, scope);},
      value => {setDevices(value); setDevice(undefined);});
  };
  const cancel = () => {
    if (!stageCurrent() || shownCodes.current) return;
    if (mode === 'confirm-disable') {setMode('disable'); return;}
    if (mode === 'confirm-remove') {setMode('main'); return;}
    if (device) {setDevice(undefined); setMode('main'); return;}
    if (mode !== 'main') {go('main'); return;}
    close();
  };
  return {
    allowed: lifetime.current() && context.current() && nativeAccountSecurityAllowed(context, kind),
    hosted: !!context.account.account.hostedAccountId,
    showing: current(held),
    label: t(kind === 'password' ? 'settings.row.password' : kind === 'twoStep' ? 'settings.row.twoStep' : 'settings.section.devices'),
    busy, error, notice, factor, enrolment, codes, copied, mode, password, nextPassword, confirmation, code, strength, matching,
    canSave, canVerify, devices, device, deviceName, go, open, cancel,
    setPassword: edit(setPassword), setNextPassword: edit(setNextPassword), setConfirmation: edit(setConfirmation),
    setCode: edit(setCode), setDeviceName: edit(setDeviceName),
    savePassword, submitFactor, verifyFactor, enrol, disable, signOutAll, pickDevice, removeDevice, signOutDevice, renameDevice, copyCodes,
    dismiss: () => {if (!shownCodes.current && current(held)) close();},
    codesSaved: () => {if (current(held)) close();},
    clearError: () => {if (current(held)) setError(undefined);},
    retry: () => {if (current(held)) load(held);},
  };
}

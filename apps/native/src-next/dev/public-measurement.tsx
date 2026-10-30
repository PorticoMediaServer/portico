import React, {useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode} from 'react';
import {Linking} from 'react-native';
import {AccountEntry} from '../screens/accounts/AccountEntry';
import {nativeMeasurementLaunch, type NativeMeasurementLaunch} from './measurement-launch';

type Launch = Readonly<{ready: boolean; selection: NativeMeasurementLaunch; revision: number}>;
const unresolved: Launch = Object.freeze({ready: false, selection: Object.freeze({kind: 'refused'}), revision: 0});

/** Resolve the DEV launch before any normal shell can read real authentication.
 * Only the processed public enum is retained; raw URL/props never reach a dump. */
export function useNativePublicMeasurementLaunch(): Launch {
  const [launch, setLaunch] = useState<Launch>(unresolved);
  useEffect(() => {
    let live = true, changed = false;
    const receive = (url: string | null) => {
      if (live) setLaunch(previous => Object.freeze({ready: true, selection: nativeMeasurementLaunch(url), revision: previous.revision + 1}));
    };
    const subscription = Linking.addEventListener('url', event => {changed = true; receive(event.url);});
    void Linking.getInitialURL().then(url => {if (!changed) receive(url);}, () => {
      if (live && !changed) setLaunch(previous => Object.freeze({ready: true, selection: Object.freeze({kind: 'refused'}), revision: previous.revision + 1}));
    });
    return () => {live = false; subscription.remove();};
  }, []);
  return launch;
}

const noPublicAdmission = (): never => {throw Object.assign(new Error('native_public_measurement_has_no_auth_admission'), {code: 'cancelled'});};

/** A controller-only boundary: no new View, style, registry or presenter. The
 * public branch is the actual welcome AccountEntry; normal auth never mounts
 * while launch selection is unresolved or refused. Collector installation is
 * supplied only by App's DEV branch after its owned module is available. */
export function NativePublicMeasurementGate({renderNormal, install}: Readonly<{
  renderNormal: () => ReactNode;
  install: (isPublicCurrent: () => boolean) => () => void;
}>) {
  const launch = useNativePublicMeasurementLaunch(), current = useRef(launch), live = useRef(false);
  const welcome = useMemo(() => ({launch, retired: false}), [launch]);
  const retirement = useRef<{welcome: typeof welcome; dispose: () => void} | undefined>(undefined);
  const onStageChange = useMemo(() => (stage: string) => {
    if (stage === 'welcome') return;
    welcome.retired = true;
    if (retirement.current?.welcome === welcome) retirement.current.dispose();
  }, [welcome]);
  current.current = launch;
  useLayoutEffect(() => {live.current = true; return () => {live.current = false;};}, []);
  useLayoutEffect(() => {
    if (!launch.ready || launch.selection.kind !== 'public' || welcome.retired) return;
    const dispose = install(() => live.current && current.current === launch && !welcome.retired && launch.selection.kind === 'public');
    let disposed = false;
    const retire = () => {if (!disposed) {disposed = true; dispose();}};
    retirement.current = {welcome, dispose: retire};
    return retire;
  }, [install, launch, welcome]);
  if (!launch.ready || launch.selection.kind === 'refused') return null;
  if (launch.selection.kind === 'normal') return renderNormal();
  return <AccountEntry key={launch.revision} initialStage="welcome" initialAddress="" onStageChange={onStageChange} onComplete={noPublicAdmission}/>;
}

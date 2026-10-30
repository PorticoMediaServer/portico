import React, {useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {View, useWindowDimensions} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Dialog} from '../../kit/overlays/surface';
import {Button} from '../../kit/controls';
import {LoadingPaint, SkeletonBlock, StateView} from '../../kit/states';
import {NativeReadingGuestHost} from '../read/NativeReadingGuestHost';
import type {createRememberedReadingOwner} from '../../shell/remembered-reading-owner';

type Reading = ReturnType<typeof createRememberedReadingOwner>;
/** This page borrows one exact root-owned publication, never a shell session. */
export function RememberedReadingScreen({reading, noticeCopy, current: rootCurrent, onBack}: {reading: Reading; noticeCopy: Readonly<{title: string; body: string}>; current: () => boolean; onBack: () => void}) {
  const dimensions = useWindowDimensions(), insets = useSafeAreaInsets();
  const active = useRef<{live: boolean} | undefined>(undefined);
  const [ticket] = useState(() => ({live: true}));
  const [notice, setNotice] = useState<object | undefined>();
  const noticeRef = useRef<object | undefined>(undefined);
  const snapshot = useSyncExternalStore(reading.owner.subscribe, reading.owner.snapshot, reading.owner.snapshot);
  const pageCurrent = () => ticket.live && active.current === ticket && rootCurrent() && ticket.live;
  const current = () => pageCurrent() && reading.owner.current() && pageCurrent();
  useLayoutEffect(() => {active.current = ticket;
    return () => {ticket.live = false; noticeRef.current = undefined; if (active.current === ticket) active.current = undefined; void reading.owner.dispose().catch(() => {});};}, [reading, ticket]);
  useEffect(() => {void Promise.resolve().then(() => reading.owner.ensure()).catch(() => {});}, [reading]);
  const openNotice = () => {if (!current() || noticeRef.current) return; const captured = {}; noticeRef.current = captured; setNotice(captured);};
  const closeNotice = (captured: object) => {if (!current() || noticeRef.current !== captured) return; noticeRef.current = undefined; setNotice(undefined);};
  const back = () => {if (!pageCurrent()) return; ticket.live = false; noticeRef.current = undefined; void reading.owner.dispose().catch(() => {}); onBack();};
  if (snapshot.phase === 'ready') return <View style={{flex: 1}}>
    <NativeReadingGuestHost owner={reading.owner} active={!notice} preferences={reading.preferences} onBack={back} onSettings={openNotice}/>
    {notice ? <Dialog open onOpenChange={open => {if (!open) closeNotice(notice);}} title={noticeCopy.title}
      description={noticeCopy.body} showClose={false} dismissOnOutsidePress={false} dismissOnBack={false}
      footer={<Button onPress={() => closeNotice(notice)}>{defaultI18n.t('action.close')}</Button>}>{null}</Dialog> : null}
  </View>;
  if (snapshot.phase === 'idle' || snapshot.phase === 'opening') return <View style={{flex: 1}}>
    <LoadingPaint ready={false} skeleton={<SkeletonBlock width="100%" style={{flex: 1}}/>}/>
    <View pointerEvents="box-none" style={{position: 'absolute', left: 12, top: insets.top + 8, zIndex: 20}}>
      <Button testID="native-read-host-back" variant="icon" size="compact" icon="i-left" iconOnly accessibilityLabel={defaultI18n.t('action.back')}
        focusRadius={dimensions.width >= 700 ? 22 : 19} onPress={back} style={{height: dimensions.width >= 700 ? 44 : 38, width: dimensions.width >= 700 ? 44 : 38, paddingHorizontal: 0}}/>
    </View>
  </View>;
  return <View style={{flex: 1}}><StateView variant="centered" title={defaultI18n.t('next.states.pageFailed')}
    body={defaultI18n.t('next.states.pageFailedBody')} primaryAction={{id: 'back', label: defaultI18n.t('action.back'), onPress: back}}/></View>;
}

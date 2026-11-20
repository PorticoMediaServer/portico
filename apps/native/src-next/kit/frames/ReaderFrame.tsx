import React, {type ReactNode} from 'react';
import {useNativeStackHosted} from '../navigation/stack-slot';
import {CoveredLayer} from '../focus/CoveredLayer';
import {Layer} from '../shell/Layer';
import {LoadingPaint, SkeletonBlock} from '../states';
import type {TopBarBack} from '../layout/TopBar';
import {ReaderBack} from '../reading/ReaderBack';
import {PageStateFrame, type PageStateProps} from './PageStateFrame';

/** The native window for a reader guest, with a Back control while the guest cannot draw its own. */
export function ReaderFrame({active, back, state, children}: {
  active: boolean; back?: TopBarBack; state?: Omit<PageStateProps, 'icon'>; children?: ReactNode;
}) {
  const stacked = useNativeStackHosted();
  const ready = children != null;
  return <CoveredLayer covered={!active} style={{position: 'absolute', inset: 0, opacity: active || stacked ? 1 : 0}}>
    <Layer>
      {state ? <PageStateFrame icon="i-alert" {...state}/> : <LoadingPaint ready={ready} immediate={ready}
        skeleton={<SkeletonBlock width="100%" style={{flex: 1}}/>}>{children}</LoadingPaint>}
      {back && <ReaderBack {...back}/>}
    </Layer>
  </CoveredLayer>;
}

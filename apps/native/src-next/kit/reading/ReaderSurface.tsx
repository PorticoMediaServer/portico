import React, {type ReactNode} from 'react';
import {useNativeStackHosted} from '../navigation/stack-slot';
import {CoveredLayer} from '../focus/CoveredLayer';
import {Layer} from '../shell/Layer';
import {LoadingPaint, SkeletonBlock} from '../states';

export function ReaderSurface({active, children}: {active: boolean; children: ReactNode}) {
  const stacked = useNativeStackHosted();
  return <CoveredLayer covered={!active} style={{position: 'absolute', inset: 0, opacity: active || stacked ? 1 : 0}}>
    <Layer>{children}</Layer>
  </CoveredLayer>;
}

export function ReaderLoading({ready, children}: {ready: boolean; children?: ReactNode}) {
  return <LoadingPaint ready={ready} immediate={ready} skeleton={<SkeletonBlock width="100%" style={{flex: 1}}/>}>{children}</LoadingPaint>;
}

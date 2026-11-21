import React from 'react';
import {EqualizerFrame, type EqualizerFrameProps} from '../../../kit/frames/EqualizerFrame';
import {useEqualizer} from './useEqualizer';

export type EqualizerPageProps = Pick<EqualizerFrameProps, 'onBack' | 'copy' | 'response' | 'focusScope' | 'active'> & Parameters<typeof useEqualizer>[0];

export function EqualizerPage({store, reportError, ...frame}: EqualizerPageProps) {
  const model = useEqualizer({store, reportError, active: frame.active});
  return <EqualizerFrame {...frame} {...model}/>;
}

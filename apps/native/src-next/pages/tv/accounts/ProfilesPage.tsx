import React from 'react';
import {TVAccountCanvas} from '../../../kit/frames/TVAuthLayout';

export function TVWhosWatching({children, testID}: {children: React.ReactNode; testID: string}) {
  return <TVAccountCanvas testID={testID} shape="round">{children}</TVAccountCanvas>;
}

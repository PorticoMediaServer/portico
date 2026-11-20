import React, {type ReactNode} from 'react';
import {BrowseFrame, type BrowseHeaderProps} from '../../../kit/frames';

export function TVSavedPage({title, tabs, body}: {title: string; tabs: BrowseHeaderProps['tabs']; body: ReactNode}) {
  return <BrowseFrame testID="native-saved-tv-frame" header={{title, tabs}} grid={body}/>;
}

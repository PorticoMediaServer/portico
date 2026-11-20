import React, {type ReactNode} from 'react';
import {BrowseFrame, type BrowseHeaderProps} from '../../../kit/frames';

export function SavedPage({title, tabs, body}: {title: string; tabs?: BrowseHeaderProps['tabs']; body: ReactNode}) {
  return <BrowseFrame header={{title, tabs}} grid={body}/>;
}

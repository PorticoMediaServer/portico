import React, {type ReactNode} from 'react';
import {BrowseFrame, type BrowseHeaderProps} from '../../../kit/frames';

export function DownloadsPage({title, back, body}: {title: string; back: BrowseHeaderProps['back']; body: ReactNode}) {
  return <BrowseFrame header={{title, back}} grid={body}/>;
}

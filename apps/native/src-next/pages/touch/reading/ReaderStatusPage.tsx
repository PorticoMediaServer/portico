import React, {type ReactNode} from 'react';
import {PageStateFrame, type PageStateProps} from '../../../kit/frames';
import {ReaderBack} from '../../../kit/reading/ReaderBack';
import type {TopBarBack} from '../../../kit/layout/TopBar';

export function ReaderStatusPage({back, state, children}: {
  back: TopBarBack; state?: Omit<PageStateProps, 'icon'>; children?: ReactNode;
}) {
  return <>
    {state ? <PageStateFrame icon="i-alert" {...state}/> : children}
    <ReaderBack {...back}/>
  </>;
}

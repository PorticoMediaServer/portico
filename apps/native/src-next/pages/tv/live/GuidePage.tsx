import React from 'react';
import {LiveFrame, type LiveFrameProps} from '../../../kit/frames';

export function TVGuidePage({title, actions, tabs, list, overlay}: Pick<LiveFrameProps, 'title' | 'actions' | 'tabs' | 'list' | 'overlay'>) {
  return <LiveFrame title={title} actions={actions} tabs={tabs} list={list} overlay={overlay} flushList testID="tv-live-guide"/>;
}

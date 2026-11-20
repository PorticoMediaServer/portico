import React from 'react';
import {LiveFrame, type LiveFrameProps} from '../../../kit/frames';

export function GuidePage({title, actions, tabs, toolbar, list, overlay}: Pick<LiveFrameProps, 'title' | 'actions' | 'tabs' | 'toolbar' | 'list' | 'overlay'>) {
  return <LiveFrame title={title} actions={actions} tabs={tabs} toolbar={toolbar} list={list} overlay={overlay} testID="phone-live-guide"/>;
}

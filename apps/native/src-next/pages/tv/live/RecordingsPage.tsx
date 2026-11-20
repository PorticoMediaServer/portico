import React from 'react';
import {LiveFrame, type LiveFrameProps} from '../../../kit/frames';

export function TVRecordingsPage({title, facts, tabs, list, overlay}: Pick<LiveFrameProps, 'title' | 'facts' | 'tabs' | 'list' | 'overlay'>) {
  return <LiveFrame title={title} facts={facts} tabs={tabs} list={list} overlay={overlay} testID="tv-live-recordings"/>;
}

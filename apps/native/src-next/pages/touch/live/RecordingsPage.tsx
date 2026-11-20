import React, {type ComponentProps} from 'react';
import {Tabs} from '../../../kit/controls';
import {LiveFrame, type LiveFrameProps} from '../../../kit/frames';

export function RecordingsPage({title, back, tabs, choices, list, footer, overlay}: Pick<LiveFrameProps, 'title' | 'back' | 'tabs' | 'list' | 'footer' | 'overlay'> & {
  choices?: Pick<ComponentProps<typeof Tabs>, 'items' | 'value' | 'onValueChange'>;
}) {
  return <LiveFrame title={title} back={back} tabs={choices ? <Tabs {...choices}
    itemProps={item => ({testID: `recordings-view-${item.value}`})}/> : tabs}
    list={list} footer={footer} overlay={overlay} testID="phone-live-recordings"/>;
}

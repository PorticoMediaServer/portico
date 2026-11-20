import React, {type ComponentProps, type ReactNode} from 'react';
import {LibrarySwitcher} from '../../../kit/browse';
import {BrowseFrame, type BrowseHeaderProps} from '../../../kit/frames';

export function BooksPage({title, library, tabs, back, toolbar, body, overlay}: {
  title: string; library: ComponentProps<typeof LibrarySwitcher>; tabs: BrowseHeaderProps['tabs'];
  back: BrowseHeaderProps['back']; toolbar: ReactNode; body: ReactNode; overlay: ReactNode;
}) {
  return <>
    <BrowseFrame testID="phone-reading-library" header={{title, back, tabs, switcher: <LibrarySwitcher {...library}/>}}
      toolbar={toolbar} grid={body}/>
    {overlay}
  </>;
}

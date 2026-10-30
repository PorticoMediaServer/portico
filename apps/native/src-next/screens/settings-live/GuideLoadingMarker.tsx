import React from 'react';
import {LoadingPaint, SkeletonBlock} from '../../kit/states';
import {guideLoadingMarkerBox} from './guide-loading-marker';

/** The cell's programme loading marker as a pure wrapper over the approved
 * LoadingPaint: ready=false keeps the shared 300ms quiet window and the static
 * paint, with the supplied skeleton in the cell's exact absolute footprint.
 * Nothing here owns a timer, a clock or a visibility decision, and the cell's
 * ready/failed/focus branches and read/retry lifetime are untouched. */
export function GuideLoadingMarker({cellWidth, rowHeight, compact, left, testID}: Readonly<{
  cellWidth: number; rowHeight: number; compact: boolean; left: number; testID: string;
}>) {
  const box = guideLoadingMarkerBox(cellWidth, rowHeight, compact);
  return <LoadingPaint testID={testID} ready={false} style={{position: 'absolute', left, top: box.top, width: box.width, height: box.height}}
    skeleton={<SkeletonBlock width={box.width} height={box.height} radius={box.radius}/>}/>;
}

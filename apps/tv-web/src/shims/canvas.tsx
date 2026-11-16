import React, {useEffect, useState} from 'react';
import * as Web from 'react-native-web';

/**
 * The 1920×1080 design canvas the TV screens are drawn for. LG and Samsung run a web app at
 * 1920×1080 on every set, 4K ones included, so on a set this is the window. Anywhere else (a
 * desktop browser while developing, a test's window) the canvas is scaled to fit, and the
 * screens still measure 1920×1080 (`useWindowDimensions` and `Dimensions` in the shim).
 */
export const CANVAS = Object.freeze({width: 1920, height: 1080, scale: 1, fontScale: 1});

function fit(): number {
  return Math.min(window.innerWidth / CANVAS.width, window.innerHeight / CANVAS.height) || 1;
}

export function Canvas({children}: {children: React.ReactNode}): React.ReactElement {
  const [scale, setScale] = useState(fit);
  useEffect(() => {
    const resized = () => setScale(fit());
    window.addEventListener('resize', resized);
    return () => window.removeEventListener('resize', resized);
  }, []);
  return <Web.View style={{position: 'absolute', left: 0, top: 0, width: CANVAS.width, height: CANVAS.height, overflow: 'hidden',
    transformOrigin: 'top left', transform: scale === 1 ? undefined : [{scale}]} as never}>{children}</Web.View>;
}

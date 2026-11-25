import React, {useEffect, useState} from 'react';

/** A page kept on screen until `until` (a Date.now() time), then nothing. It ends on its own timer, so it never outlives
 * `until` when nothing else redraws its host (the watch page during an Up next hand-over whose next item is paused). */
export function KeptPage({until, children}: {until: number; children: React.ReactElement}) {
  const [over, setOver] = useState(() => Date.now() >= until);
  useEffect(() => {
    if (over) return;
    const timer = setTimeout(() => setOver(true), Math.max(0, until - Date.now()));
    return () => clearTimeout(timer);
  }, [until, over]);
  return over ? null : children;
}

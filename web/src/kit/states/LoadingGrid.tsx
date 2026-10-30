import {useEffect, useState} from 'react';
import {GridSkeleton} from './Skeletons';
/** The loading instance owns its 300ms quiet delay; a new request starts afresh. */
export function LoadingGrid({width}: {width: number}) {
  const [started] = useState(Date.now), [elapsed, setElapsed] = useState(0);
  const [viewport, setViewport] = useState(() => ({height: window.innerHeight, scrollTop: window.scrollY}));
  useEffect(() => {
    const timer = setTimeout(() => setElapsed(Date.now() - started), Math.max(0, 300 - (Date.now() - started)));
    return () => clearTimeout(timer);
  }, [started]);
  useEffect(() => {
    const update = () => setViewport({height: window.innerHeight, scrollTop: window.scrollY});
    window.addEventListener('resize', update); window.addEventListener('scroll', update, {passive: true});
    return () => {window.removeEventListener('resize', update);window.removeEventListener('scroll', update);};
  }, []);
  return <GridSkeleton elapsedMs={elapsed} window={{width, ...viewport}} itemCount={21}/>;
}

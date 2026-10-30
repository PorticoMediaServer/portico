import {useEffect, useRef, useState} from 'react';
import {KeyboardShortcuts} from '../kit/states';
import type {ShellPlaybackBinding} from './Playback';

/** The player owns its help while expanded; the shell owns the same overlay elsewhere. */
export function GlobalShortcuts({playback}: {playback: Pick<ShellPlaybackBinding, 'skips'>}) {
  const [open, setOpen] = useState(false);
  const returnFocus = useRef<HTMLElement | null>(null);
  useEffect(() => {
    const keydown = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.repeat || event.ctrlKey || event.metaKey || event.altKey || event.key !== '?') return;
      const target = event.target;
      if (target instanceof Element && target.closest('input,textarea,select,[contenteditable]:not([contenteditable="false"]),[role="slider"],[role="menu"],[role="dialog"]')) return;
      const surfaces = document.querySelectorAll('[role="dialog"],[role="menu"],[aria-modal="true"],[data-watch-screen]');
      if ([...surfaces].some(surface => !surface.closest('[hidden],[aria-hidden="true"]') && surface.getClientRects().length > 0
        && getComputedStyle(surface).visibility !== 'hidden')) return;
      event.preventDefault();
      returnFocus.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
      setOpen(true);
    };
    // Window runs after the player's document handler, so an owned key stays owned.
    window.addEventListener('keydown', keydown);
    return () => window.removeEventListener('keydown', keydown);
  }, []);
  return <KeyboardShortcuts open={open} onOpenChange={setOpen} initialSection="anywhere"
    skipBackSeconds={playback.skips.back} skipForwardSeconds={playback.skips.forward} returnFocusRef={returnFocus}/>;
}

import {useEffect, useRef, useState} from 'react';
import {KeyboardShortcuts} from '../kit/states';
import {router} from '../router';
import type {ShellPlaybackBinding} from './Playback';

const IGNORED_TARGET = 'input,textarea,select,[contenteditable]:not([contenteditable="false"]),[role="slider"],[role="menu"],[role="dialog"]';
const BLOCKING_SURFACES = '[role="dialog"],[role="menu"],[aria-modal="true"],[data-watch-screen]';
const SEARCH_INPUTS = '[data-route-content] input[type="search"],main input[type="search"]';

function isVisibleElement(element: Element): boolean {
  if (element.closest('[hidden],[inert],[aria-hidden="true"]')) return false;
  if (typeof element.getClientRects === 'function' && element.getClientRects().length === 0) return false;
  return typeof getComputedStyle !== 'function' || getComputedStyle(element).visibility !== 'hidden';
}

export type GlobalShortcutKind = 'help' | 'search';

/** Decide whether a global keydown belongs to the shell's `?` (help) or `/` (search) shortcut. */
export function globalShortcutFor(event: Pick<KeyboardEvent, 'defaultPrevented' | 'repeat' | 'ctrlKey' | 'metaKey' | 'altKey' | 'key' | 'target'>,
  doc: Pick<Document, 'querySelectorAll'> = document): GlobalShortcutKind | null {
  if (event.defaultPrevented || event.repeat || event.ctrlKey || event.metaKey || event.altKey) return null;
  const kind: GlobalShortcutKind | null = event.key === '?' ? 'help' : event.key === '/' ? 'search' : null;
  if (!kind) return null;
  const target = event.target;
  if (typeof Element !== 'undefined' && target instanceof Element && target.closest(IGNORED_TARGET)) return null;
  const surfaces = doc.querySelectorAll(BLOCKING_SURFACES);
  if ([...surfaces].some(isVisibleElement)) return null;
  return kind;
}

/** Focus an already-rendered search field if visible on the current route; returns true when focused. */
export function focusVisibleSearchInput(doc: Pick<Document, 'querySelectorAll'> = document): boolean {
  const input = [...doc.querySelectorAll<HTMLInputElement>(SEARCH_INPUTS)].find(el => !el.disabled && isVisibleElement(el));
  if (!input) return false;
  input.focus();
  if (typeof input.select === 'function') input.select();
  return true;
}

/** The player owns its help while expanded; the shell owns the same overlay elsewhere. */
export function GlobalShortcuts({playback, onSearch}: {playback: Pick<ShellPlaybackBinding, 'skips'>; onSearch?: () => void}) {
  const [open, setOpen] = useState(false);
  const returnFocus = useRef<HTMLElement | null>(null);
  const searchHandler = useRef(onSearch);
  searchHandler.current = onSearch;
  useEffect(() => {
    const keydown = (event: KeyboardEvent) => {
      const kind = globalShortcutFor(event);
      if (!kind) return;
      event.preventDefault();
      if (kind === 'search') {
        if (focusVisibleSearchInput()) return;
        if (searchHandler.current) searchHandler.current();
        else void router.navigate({to: '/search', search: {q: ''}});
        return;
      }
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

/** Contextual glyphs from desktop-player's own sprite, shared by its drawn notices/cards. */
export function WatchPromptGlyph({kind, size = 20, className}: {kind: 'dismiss' | 'next' | 'play'; size?: number; className?: string}) {
  return <svg className={className} width={size} height={size} viewBox="0 0 24 24" aria-hidden="true" focusable="false">
    {kind === 'dismiss' ? <path fill="none" stroke="currentColor" strokeWidth={2.2} strokeLinecap="round" d="M6.5 6.5l11 11M17.5 6.5l-11 11" />
      : <path fill="currentColor" d={kind === 'play' ? 'M7 4.8v14.4c0 .8.9 1.3 1.6.9l11.4-7.2a1 1 0 0 0 0-1.8L8.6 3.9C7.9 3.5 7 4 7 4.8z'
        : 'M18.5 5.2a1.1 1.1 0 0 0-2.2 0v13.6a1.1 1.1 0 0 0 2.2 0zm-3.9 7.6a1 1 0 0 0 0-1.6L5.9 5.1A1 1 0 0 0 4.3 6v12a1 1 0 0 0 1.6.9z'} />}
  </svg>;
}

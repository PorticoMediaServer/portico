import {defaultI18n, type MessageId} from '@i18n';

export type ShortcutSectionId = 'player' | 'live' | 'guide' | 'anywhere';
export type ShortcutKey = 'Space' | 'K' | 'left' | 'J' | 'right' | 'L' | 'up' | 'down' | 'M' | 'F' | 'I' | 'Z' | 'C' | 'G' | 'H' | 'zero' | 'nine' | 'Home' | 'End' | 'less' | 'greater' | 'ShiftN' | 'ShiftP' | 'PageUp' | 'PageDown' | 'comma' | 'period' | 'Esc' | 'help' | 'Enter' | 'W' | 'R' | 'N' | 'slash';
export type ShortcutRow = Readonly<{id: string; keys: readonly ShortcutKey[]; separator?: 'or' | 'range'; label: string}>;
export type ShortcutSection = Readonly<{id: ShortcutSectionId; labelKey: MessageId; rows: readonly ShortcutRow[]}>;

/** The amounts are the actual Settings values supplied by the player. */
export function keyboardShortcutSections(skipBackSeconds: number, skipForwardSeconds: number): readonly ShortcutSection[] {
  const t = defaultI18n.t;
  const row = (id: string, keys: readonly ShortcutKey[], label: string, separator?: 'or' | 'range'): ShortcutRow => ({id, keys, label, separator});
  return [
    {id: 'player', labelKey: 'next.states.shortcutsPlayer', rows: [
      row('play', ['Space','K'], t('next.states.shortcut.play'), 'or'),
      row('back', ['left','J'], t('next.states.shortcut.back', {seconds: skipBackSeconds}), 'or'),
      row('forward', ['right','L'], t('next.states.shortcut.forward', {seconds: skipForwardSeconds}), 'or'),
      row('volume', ['up','down'], t('next.states.shortcut.volume')),
      row('mute', ['M'], t('next.states.shortcut.mute')),
      row('fullscreen', ['F'], t('next.states.shortcut.fullscreen')),
      row('pip', ['I'], t('next.states.shortcut.pip')),
      row('fit', ['Z'], t('next.states.shortcut.fit')),
      row('subtitles', ['C'], t('next.states.shortcut.subtitles')),
      row('subtitle-timing', ['G','H'], t('next.states.shortcut.subtitleTiming')),
      row('jump', ['zero','nine'], t('next.states.shortcut.jump'), 'range'),
      row('ends', ['Home','End'], t('next.states.shortcut.ends')),
      row('speed', ['less','greater'], t('next.states.shortcut.speed')),
      row('next', ['ShiftN','ShiftP'], t('next.states.shortcut.next')),
      row('chapter', ['PageUp','PageDown'], t('next.states.shortcut.chapter')),
      row('frame', ['comma','period'], t('next.states.shortcut.frame')),
      row('escape', ['Esc'], t('next.states.shortcut.escape')),
      row('help', ['help'], t('next.states.shortcut.help')),
    ]},
    {id: 'live', labelKey: 'next.states.shortcutsLive', rows: [row('channel', ['up','down'], t('next.states.shortcut.channel')), row('guide', ['G'], t('next.states.shortcut.guide'))]},
    {id: 'guide', labelKey: 'next.states.shortcutsGuide', rows: [
      row('move', ['left','right','up','down'], t('next.states.shortcut.moveGuide')),
      row('programme', ['Enter'], t('next.states.shortcut.programme')),
      row('watch', ['W'], t('next.states.shortcut.watch')),
      row('record', ['R'], t('next.states.shortcut.record')),
      row('now', ['N'], t('next.states.shortcut.now')),
    ]},
    {id: 'anywhere', labelKey: 'next.states.shortcutsAnywhere', rows: [row('search', ['slash'], t('next.states.shortcut.search')), row('help', ['help'], t('next.states.shortcut.help'))]},
  ];
}

type Block = Readonly<{id: string; weekdays: readonly number[]; startMinute: number; endMinute: number}>;
/** Split a wrapping block at midnight. The second part belongs to the next
 * weekday, including Sunday-to-Monday; start=end is a full 24-hour block. */
export function channelDaySegments<T extends Block>(blocks: readonly T[], day: number): readonly {block: T; start: number; end: number; continuation: boolean}[] {
  const segments: {block: T; start: number; end: number; continuation: boolean}[] = [];
  for (const block of blocks) {
    const overnight = block.endMinute <= block.startMinute;
    if (block.weekdays.includes(day)) segments.push({block, start: block.startMinute, end: overnight ? 1440 : block.endMinute, continuation: false});
    if (overnight && block.endMinute > 0 && block.weekdays.includes((day + 6) % 7)) segments.push({block, start: 0, end: block.endMinute, continuation: true});
  }
  return segments;
}

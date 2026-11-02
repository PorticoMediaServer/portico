/** A row's guide state from what its loaded window actually holds (LT-7). The server publishes no
 *  per-channel guide flag, so a channel whose whole complete window has no programmes is drawn as
 *  the mock-up's one "No guide information / You can still watch this channel" band. A window
 *  still loading or failed is never labelled, and a hole inside a row with programmes stays
 *  "No information". */
export function guideRowChannel<T extends Readonly<{guide: 'full' | 'partial' | 'none'}>>(channel: T, page: Readonly<{programs: readonly unknown[]; complete: boolean; failed: boolean}>): T {
  return channel.guide === 'full' && page.complete && !page.failed && page.programs.length === 0 ? {...channel, guide: 'none'} : channel;
}

import {planFixedLayout, type FixedLayoutInput} from '@portico/reader-core';

/** The renderer's own pairing rules, with at most eleven single pages or five spreads. */
export function fixedPageWindow(input: FixedLayoutInput) {
  const current = planFixedLayout(input), limit = current.spread ? 5 : 11;
  const layouts = [current];
  for (let before = 0; before < Math.floor(limit / 2); before++) {
    const page = layouts[0].previousPage;
    if (page === null) break;
    layouts.unshift(planFixedLayout({...input, page}));
  }
  while (layouts.length < limit) {
    const page = layouts.at(-1)!.nextPage;
    if (page === null) break;
    layouts.push(planFixedLayout({...input, page}));
  }
  while (layouts.length < limit) {
    const page = layouts[0].previousPage;
    if (page === null) break;
    layouts.unshift(planFixedLayout({...input, page}));
  }
  return {current, groups: layouts.map(layout => ({pages: layout.pages, current: layout.pages.includes(input.page)}))};
}

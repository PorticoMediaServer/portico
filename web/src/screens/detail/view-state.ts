/** Presentation state belongs to the captured domain lifetime, including Watch body remounts. */
export function titleView<Tab extends string>(initial: Tab, tabs: readonly Tab[], assertCurrent: () => void) {
  let defaultLayout: 'grid' | 'list' = 'grid';
  let value: Readonly<{tab: Tab; layout: 'grid' | 'list'}> = Object.freeze({tab: initial, layout: 'grid'});
  const listeners = new Set<() => void>();
  const publish = (next: typeof value) => { value = Object.freeze(next); for (const listener of listeners) listener(); };
  return {
    getSnapshot: () => value,
    subscribe: (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener); }; },
    tab(tab: Tab) { assertCurrent(); if (!tabs.includes(tab)) throw new Error('Unknown title tab'); if (tab !== value.tab) publish({...value, tab}); },
    layout(layout: 'grid' | 'list') { assertCurrent(); if (layout !== 'grid' && layout !== 'list') throw new Error('Unknown episode layout'); if (layout !== value.layout) publish({...value, layout}); },
    defaultLayout(layout: 'grid' | 'list') { assertCurrent(); defaultLayout = layout; if (layout !== value.layout) publish({...value, layout}); },
    reset() { publish({tab: initial, layout: defaultLayout}); },
    dispose() { defaultLayout = 'grid'; publish({tab: initial, layout: 'grid'}); listeners.clear(); },
  };
}

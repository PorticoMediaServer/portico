import React, {createContext, useContext} from 'react';
import {useWindowDimensions} from 'react-native';

/** N2-3: how far from the window's left edge the shell draws this page. On a tablet a pushed page
 * sits beside the rail (tablet 6 and 8), so a page that sizes itself from the window subtracts it.
 * Zero everywhere else: phones, TV, the reader and live TV, and any page the shell draws full width. */
const PageInset = createContext(0);
export const NativePageInsetProvider = PageInset.Provider;
export const usePageInset = (): number => useContext(PageInset);
/** The width this page is drawn in: the window less the shell's rail beside it. */
export function usePageWidth(): number {
  const window = useWindowDimensions(), inset = usePageInset();
  return Math.max(0, window.width - inset);
}
export const pageInsetElement = (inset: number, children: React.ReactNode) => React.createElement(PageInset.Provider, {value: inset}, children);

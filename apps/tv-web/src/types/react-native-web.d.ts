// react-native-web publishes no types. It implements React Native's API for the browser, so the
// shims here are typed with React Native's own declarations.
declare module 'react-native-web' {
  export * from 'react-native';
}

// The masked-view shim draws its mask element to markup once (`shims/masked-view.tsx`).
declare module 'react-dom/server' {
  export function renderToStaticMarkup(element: import('react').ReactElement): string;
}

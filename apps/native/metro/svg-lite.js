// PERF-15: react-native-svg without its CSS/XML-parser barrel. The package root re-exports
// `./css` (css-tree, css-select, entities: about 540 KB of the production bundle), which Portico
// never uses. metro.config.js points every `react-native-svg` import outside the package here.
// No app code uses the XML parsers either (the brand mark is prebuilt paths), so only elements
// are re-exported.
export * from 'react-native-svg/src/elements';
export {default} from 'react-native-svg/src/elements';

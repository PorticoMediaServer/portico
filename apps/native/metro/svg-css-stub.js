// PERF-15: `react-native-qrcode-svg` imports LocalSvg from `react-native-svg/css` for its logo
// option, which Portico never passes. Without this stub the CSS parsers are bundled for it.
export const LocalSvg = () => null;

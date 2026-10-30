import '@amazon-devices/react-native-kepler';

// Props the Apple TV build (react-native-tvos) accepts and the shared screens pass. The Vega
// SDK's views ignore them.
declare module '@amazon-devices/react-native-kepler' {
  interface PressableProps {
    /** tvOS parallax. Portico turns it off everywhere: scale and the ring are the focus language. */
    tvParallaxProperties?: {enabled?: boolean} | undefined;
  }
}

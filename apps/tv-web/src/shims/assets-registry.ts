// React Native's asset registry, which react-native-svg asks about numbered image sources. In a
// browser an imported image is already its address, so the registry holds nothing.
export function getAssetByID(_id: number): undefined {return undefined;}
export function registerAsset(_asset: unknown): number {return 0;}

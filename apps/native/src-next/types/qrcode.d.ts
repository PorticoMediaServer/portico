// The QR encoder react-native-qrcode-svg draws with; only what kit/qr-matrix.ts reads.
declare module 'qrcode' {
  const QRCode: {create(text: string, options: {errorCorrectionLevel: 'L' | 'M' | 'Q' | 'H'}): {modules: {size: number; data: ArrayLike<number | boolean>}}};
  export default QRCode;
}

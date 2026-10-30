import QRCode from 'qrcode';

/** A QR code's module matrix, row by row (true is a dark module), from the same encoder
 * react-native-qrcode-svg draws with. For drawing a code into a picture of our own. */
export function qrMatrix(value: string, errorCorrectionLevel: 'L' | 'M' | 'Q' | 'H'): boolean[][] {
  const {size, data} = QRCode.create(value, {errorCorrectionLevel}).modules;
  const rows: boolean[][] = [];
  for (let y = 0; y < size; y++) {
    const row: boolean[] = [];
    for (let x = 0; x < size; x++) row.push(!!data[y * size + x]);
    rows.push(row);
  }
  return rows;
}

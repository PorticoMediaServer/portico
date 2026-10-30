import {defaultI18n} from '@i18n';

type QR = Readonly<{size: number; get: (row: number, column: number) => number}>;

/** Local rendering for the invitation card; no link or identity leaves the app. */
export async function invitationPicture({qr, link, expiry, serverName, access}: {
  qr: QR; link: string; expiry: string; serverName: string; access: string;
}): Promise<Blob> {
      const canvas = document.createElement('canvas'); canvas.width = 1024; canvas.height = 1360;
      const context = canvas.getContext('2d'); if (!context) throw new Error('invitation_picture_unavailable');
      context.scale(2, 2); context.fillStyle = '#0b1822'; context.fillRect(0, 0, 512, 680);
      const blue = context.createRadialGradient(512, 0, 0, 512, 0, 520); blue.addColorStop(0, 'rgba(47,134,187,.5)'); blue.addColorStop(1, 'rgba(47,134,187,0)'); context.fillStyle = blue; context.fillRect(0, 0, 512, 680);
      const green = context.createRadialGradient(0, 680, 0, 0, 680, 460); green.addColorStop(0, 'rgba(30,91,82,.5)'); green.addColorStop(1, 'rgba(30,91,82,0)'); context.fillStyle = green; context.fillRect(0, 0, 512, 680);
      const line = (text: string, y: number, size: number, color: string, weight = 600) => {
        context.font = `${weight} ${size}px Manrope, sans-serif`; context.fillStyle = color;
        let shown = text; while (context.measureText(shown).width > 460 && shown.length) shown = shown.slice(0, -1);
        if (shown !== text) shown = shown.slice(0, -1) + '…'; context.fillText(shown, 26, y);
      };
      line('Portico', 42, 20, '#edf5fa', 800); line(expiry, 68, 12, '#bdccd6');
      line(defaultI18n.t('accounts.invitation.to'), 110, 15, '#5cb3e2'); line(serverName, 152, 36, '#edf5fa', 800); line(access, 184, 14, '#bdccd6');
      // Four-module quiet zone; integer module pixels keep the saved QR crisp.
      const moduleSize = Math.max(1, Math.floor(260 / (qr.size + 8))), tile = moduleSize * (qr.size + 8), left = (512 - tile) / 2, top = 218;
      context.fillStyle = '#fff'; context.fillRect(left, top, tile, tile); context.fillStyle = '#000';
      for (let row = 0; row < qr.size; row++) for (let column = 0; column < qr.size; column++) if (qr.get(row, column)) context.fillRect(left + (column + 4) * moduleSize, top + (row + 4) * moduleSize, moduleSize, moduleSize);
      context.font = '12px monospace'; context.fillStyle = '#edf5fa';
      let remaining = link, y = 518;
      while (remaining && y < 650) {let length = remaining.length; while (length > 1 && context.measureText(remaining.slice(0, length)).width > 460) length--; context.fillText(remaining.slice(0, length), 26, y); remaining = remaining.slice(length); y += 18;}
      return await new Promise<Blob>((resolve, reject) => canvas.toBlob(result => result ? resolve(result) : reject(new Error('invitation_picture_unavailable')), 'image/png'));
}

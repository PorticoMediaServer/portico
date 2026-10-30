import {DemuxError} from './types';

/** AudioSpecificConfig rates are core rates for explicit SBR/PS, not decoded output rates. */
export function aacConfig(data: Uint8Array): {objectType: number; sampleRate: number; channels: number; framesPerPacket: number} {
  let at=0;
  const bits=(n:number)=>{if(at+n>data.length*8)throw new DemuxError('Truncated AAC configuration.');let value=0;for(let i=0;i<n;i++,at++)value=value*2+((data[at>>3]!>>(7-(at&7)))&1);return value;};
  const object=()=>{const v=bits(5);return v===31?32+bits(6):v;};
  const rate=()=>{const i=bits(4);const r=i===15?bits(24):[96000,88200,64000,48000,44100,32000,24000,22050,16000,12000,11025,8000,7350][i];if(!r||r>768000)throw new DemuxError('Invalid AAC sample rate.');return r;};
  const objectType=object();let coreObject=objectType;const coreRate=rate();let sampleRate=coreRate;const layout=bits(4);
  let channels=[0,1,2,3,4,5,6,8][layout]??0;
  if(objectType===5||objectType===29){sampleRate=rate();coreObject=object();if(objectType===29&&channels===1)channels=2;}
  const shortFrames=at<data.length*8?bits(1):0;
  if(objectType!==5&&objectType!==29) {
    // Implicit backward-compatible SBR/PS signaling follows the core configuration.
    const begin=at;
    for(let bit=begin;bit+17<=Math.min(data.length*8,256);bit++){
      at=bit;if(bits(11)!==0x2b7)continue;
      if(object()!==5||!bits(1))break;
      sampleRate=rate();
      if(at+12<=data.length*8&&bits(11)===0x548&&bits(1)&&channels===1)channels=2;
      break;
    }
  }
  const coreFrames=coreObject===23||coreObject===39?(shortFrames?480:512):(shortFrames?960:1024);
  return {objectType,sampleRate,channels,framesPerPacket:Math.round(coreFrames*sampleRate/coreRate)};
}

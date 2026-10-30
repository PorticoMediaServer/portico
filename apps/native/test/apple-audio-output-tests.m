#import <Foundation/Foundation.h>
#import <AVFoundation/AVFoundation.h>
#import "../Native/PorticoEqualizer.h"
#include <math.h>

static BOOL Run(double rate,AVAudioChannelCount channels,BOOL eq,float preamp,BOOL bitExact,int bandIndex,float bandGain,double frequency) {
 AVAudioEngine *engine=[AVAudioEngine new];AVAudioPlayerNode *node=[AVAudioPlayerNode new];
 AVAudioUnitEQ *filter=[[AVAudioUnitEQ alloc]initWithNumberOfBands:10];AVAudioFormat *format=channels==8?[[AVAudioFormat alloc]initWithCommonFormat:AVAudioPCMFormatFloat32 sampleRate:rate interleaved:NO channelLayout:[[AVAudioChannelLayout alloc]initWithLayoutTag:kAudioChannelLayoutTag_MPEG_7_1_A]]:[[AVAudioFormat alloc]initStandardFormatWithSampleRate:rate channels:channels];
 // This is an offline graph test. Disconnect hardware before requesting the main mixer,
 // whose first access can otherwise initialize RemoteIO on an iOS simulator.
 NSError *error=nil;if(![engine enableManualRenderingMode:AVAudioEngineManualRenderingModeOffline format:format maximumFrameCount:1024 error:&error]){fprintf(stderr,"manual rendering: %s\n",error.localizedDescription.UTF8String);return NO;}
 [engine attachNode:node];[engine attachNode:filter];[engine connect:node to:filter format:format];[engine connect:filter to:engine.mainMixerNode format:format];
 float bands[10]={0};bands[bandIndex]=bandGain;PorticoConfigureEqualizer(filter,eq,preamp,bands);
 [engine connect:engine.mainMixerNode to:engine.outputNode format:format];
 if(PorticoEqualizerApplied(filter,engine)!=eq){fprintf(stderr,"actual EQ graph state differs from configured bypass\n");return NO;}
 enum{N=65536};float *reference=calloc((size_t)N*channels,sizeof(float));
 /* Two consecutive buffers on the production helper's node chain. The join is frame N/2. */
 for(int deck=0;deck<2;deck++){
  AVAudioPCMBuffer *buffer=[[AVAudioPCMBuffer alloc]initWithPCMFormat:format frameCapacity:N/2];buffer.frameLength=N/2;
  for(AVAudioChannelCount ch=0;ch<channels;ch++)for(int i=0;i<N/2;i++){
   int frame=deck*N/2+i;float value=bitExact?(float)(((frame*7919+(int)ch*17)%131071)-65535)/8388608.f:(float)(.005*sin(2*M_PI*frequency*frame/rate));
   buffer.floatChannelData[ch][i]=value;reference[(size_t)ch*N+frame]=value;
  }[node scheduleBuffer:buffer completionHandler:nil];
 }
 if(![engine startAndReturnError:&error]){fprintf(stderr,"engine start: %s\n",error.localizedDescription.UTF8String);free(reference);return NO;}[node play];
 AVAudioPCMBuffer *out=[[AVAudioPCMBuffer alloc]initWithPCMFormat:format frameCapacity:1024];int offset=0,retries=0;BOOL ok=YES,floatExact=YES;double inputEnergy=0,outputEnergy=0,maxError=0;
 while(offset<N&&retries++<10000){int count=MIN(1024,N-offset);AVAudioEngineManualRenderingStatus status=[engine renderOffline:count toBuffer:out error:&error];
  if(status==AVAudioEngineManualRenderingStatusCannotDoInCurrentContext)continue;
  if(status!=AVAudioEngineManualRenderingStatusSuccess||out.frameLength!=count){ok=NO;break;}
  for(AVAudioChannelCount ch=0;ch<channels;ch++)for(int i=0;i<count;i++){
   float input=reference[(size_t)ch*N+offset+i],sample=out.floatChannelData[ch][i];
   if(memcmp(&input,&sample,sizeof(float)))floatExact=NO;
   if(bitExact&&llround(input*8388608.0)!=llround(sample*8388608.0))ok=NO;
   maxError=MAX(maxError,fabs((double)sample-input));if(offset+i>=N/2){inputEnergy+=input*input;outputEnergy+=sample*sample;}
  }offset+=count;
 }
 double gainDb=inputEnergy>0?10*log10(outputEnergy/inputEnergy):NAN;
 if(!bitExact)ok=ok&&fabs(gainDb-(bandGain+preamp))<.2;
 printf("{\"boundary\":\"offline AVAudioEngine output; physical output unverified\",\"rate\":%.0f,\"channels\":%u,\"eq\":%s,\"bandHz\":%.0f,\"bandGainDb\":%.1f,\"preamp\":%.1f,\"frames\":%d,\"joinFrame\":%d,\"pcm24ExactRequired\":%s,\"float32Exact\":%s,\"maxError\":%.12g,\"gainDb\":%.6f,\"pass\":%s}\n",rate,channels,eq?"true":"false",frequency,bandGain,preamp,offset,N/2,bitExact?"true":"false",floatExact?"true":"false",maxError,gainDb,ok?"true":"false");
 [engine stop];free(reference);return ok&&offset==N;
}
int main(void){@autoreleasepool{
 BOOL ok=Run(44100,2,NO,0,YES,5,6,1000);ok=Run(96000,8,NO,0,YES,5,6,1000)&&ok;
 const double frequencies[10]={31,62,125,250,500,1000,2000,4000,8000,16000};
 const float gains[10]={12,-12,.5,-.5,6,-6,12,-12,.5,-.5};
 for(int i=0;i<10;i++)ok=Run(48000,2,YES,0,NO,i,gains[i],frequencies[i])&&ok;
 ok=Run(48000,2,YES,-6,NO,5,6,1000)&&ok;return ok?0:1;
}}

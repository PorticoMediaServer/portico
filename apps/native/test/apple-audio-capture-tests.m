#import <Foundation/Foundation.h>
#import <AVFoundation/AVFoundation.h>
#import "../Native/PorticoAudioCapture.h"
#include <math.h>
#include <unistd.h>

static BOOL waitFor(BOOL (^ready)(void)) {
 NSDate *deadline=[NSDate dateWithTimeIntervalSinceNow:5];
 while(!ready()&&deadline.timeIntervalSinceNow>0)[NSRunLoop.currentRunLoop runUntilDate:[NSDate dateWithTimeIntervalSinceNow:.005]];
 return ready();
}

int main(int argc,const char **argv){@autoreleasepool{
 if(argc!=3)return 2;
 double rate=atof(argv[1]);AVAudioChannelCount channels=(AVAudioChannelCount)atoi(argv[2]);
 NSString *path=NSProcessInfo.processInfo.environment[@"PORTICO_AUDIO_OUTPUT_CAPTURE"];
 if(!path.length||![path hasPrefix:@"/"]||!((rate==44100&&channels==2)||(rate==96000&&channels==8)))return 2;
 [NSFileManager.defaultManager removeItemAtPath:path error:nil];
 [NSFileManager.defaultManager removeItemAtPath:[path stringByAppendingString:@".json"] error:nil];
 AVAudioEngine *engine=[AVAudioEngine new];AVAudioPlayerNode *node=[AVAudioPlayerNode new];
 AVAudioFormat *format=channels==8?[[AVAudioFormat alloc]initWithCommonFormat:AVAudioPCMFormatFloat32 sampleRate:rate interleaved:NO channelLayout:[[AVAudioChannelLayout alloc]initWithLayoutTag:kAudioChannelLayoutTag_MPEG_7_1_A]]:[[AVAudioFormat alloc]initStandardFormatWithSampleRate:rate channels:channels];
 NSError *error=nil;
 if(![engine enableManualRenderingMode:AVAudioEngineManualRenderingModeOffline format:format maximumFrameCount:1024 error:&error])return 1;
 [engine attachNode:node];[engine connect:node to:engine.mainMixerNode format:format];[engine connect:engine.mainMixerNode to:engine.outputNode format:format];
 enum{Frames=16384};
 float *reference=calloc((size_t)Frames*channels,sizeof(float));if(!reference)return 1;
 for(int half=0;half<2;half++){
  AVAudioPCMBuffer *buffer=[[AVAudioPCMBuffer alloc]initWithPCMFormat:format frameCapacity:Frames/2];buffer.frameLength=Frames/2;
  for(AVAudioChannelCount c=0;c<channels;c++)for(int i=0;i<Frames/2;i++){
   int frame=half*Frames/2+i;float value=(float)(((frame*7919+(int)c*17)%131071)-65535)/8388608.f;
   buffer.floatChannelData[c][i]=value;reference[(size_t)c*Frames+frame]=value;
  }
  [node scheduleBuffer:buffer completionHandler:nil];
 }
 PorticoAudioCapture *capture=[PorticoAudioCapture new];[capture start:engine.mainMixerNode];
 if(![engine startAndReturnError:&error]){free(reference);return 1;}[node play];
 AVAudioPCMBuffer *output=[[AVAudioPCMBuffer alloc]initWithPCMFormat:format frameCapacity:1024];int rendered=0,attempts=0;BOOL ok=YES;
 while(rendered<Frames&&attempts++<100){
  AVAudioEngineManualRenderingStatus status=[engine renderOffline:MIN(1024,Frames-rendered) toBuffer:output error:&error];
  if(status==AVAudioEngineManualRenderingStatusCannotDoInCurrentContext)continue;
  if(status!=AVAudioEngineManualRenderingStatusSuccess){ok=NO;break;}rendered+=output.frameLength;
  // An offline renderer can outrun the bounded real-time capture writer. Pace this test's
  // supplied buffers; dropped blocks still fail through the production report.
  usleep(5000);
 }
 // Taps deliver whole OS-sized blocks. Render bounded silence after the source ends so
 // its final partial block reaches the writer; count this padding separately.
 int drainFrames=0,drainAttempts=0;
 while(ok&&[[capture snapshot][@"frames"] longLongValue]<Frames&&drainFrames<rate&&drainAttempts++<100){
  AVAudioEngineManualRenderingStatus status=[engine renderOffline:1024 toBuffer:output error:&error];
  if(status==AVAudioEngineManualRenderingStatusCannotDoInCurrentContext)continue;
  if(status!=AVAudioEngineManualRenderingStatusSuccess){ok=NO;break;}
  drainFrames+=output.frameLength;usleep(5000);
 }
 ok=waitFor(^BOOL{return [[capture snapshot][@"frames"] longLongValue]>=Frames||[[capture snapshot][@"failed"] boolValue];})&&ok;
 NSDictionary *observed=[capture snapshot];[capture stop];[engine stop];
 NSString *reportPath=[path stringByAppendingString:@".json"];
 ok=waitFor(^BOOL{return [NSFileManager.defaultManager fileExistsAtPath:reportPath];})&&ok;
 NSData *reportData=[NSData dataWithContentsOfFile:reportPath];
 NSDictionary *report=reportData?[NSJSONSerialization JSONObjectWithData:reportData options:0 error:nil]:nil;
 AVAudioFile *file=[[AVAudioFile alloc]initForReading:[NSURL fileURLWithPath:path] commonFormat:AVAudioPCMFormatFloat32 interleaved:NO error:&error];
 AVAudioPCMBuffer *captured=file?[[AVAudioPCMBuffer alloc]initWithPCMFormat:file.processingFormat frameCapacity:Frames]:nil;
 BOOL exact=NO;
 if(file&&[file readIntoBuffer:captured error:&error]&&file.length==[report[@"frames"] longLongValue]&&file.length>=Frames&&file.length<=Frames+drainFrames&&captured.frameLength==Frames&&captured.format.channelCount==channels&&captured.format.sampleRate==rate){
  exact=YES;for(AVAudioChannelCount c=0;c<channels;c++)if(memcmp(captured.floatChannelData[c],reference+(size_t)c*Frames,Frames*sizeof(float)))exact=NO;
 }
 ok=ok&&exact&&rendered==Frames&&report&&![[observed objectForKey:@"failed"] boolValue]&&![[report objectForKey:@"failed"] boolValue];
 printf("{\"boundary\":\"production main-mixer tap and CAF readback; offline supplied buffers; physical output unverified\",\"rate\":%.0f,\"channels\":%u,\"sourceFrames\":%d,\"capturedFrames\":%lld,\"renderedDrainFrames\":%d,\"joinFrame\":%d,\"sourceFloat32Exact\":%s,\"droppedOrFailed\":%s,\"pass\":%s}\n",rate,channels,Frames,[report[@"frames"] longLongValue],drainFrames,Frames/2,exact?"true":"false",[report[@"failed"] boolValue]?"true":"false",ok?"true":"false");
 free(reference);return ok?0:1;
}}

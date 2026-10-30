#import "PorticoAudioCapture.h"
#if DEBUG
#include <stdatomic.h>
@implementation PorticoAudioCapture {
 AVAudioMixerNode *_mixer;AVAudioFile *_file;dispatch_queue_t _writer;NSString *_path,*_error;
 atomic_int _pending;atomic_bool _failed;atomic_llong _frames;BOOL _started;double _sampleRate;NSUInteger _channels;
}
- (instancetype)init {if((self=[super init]))_writer=dispatch_queue_create("tv.getportico.audio.capture",DISPATCH_QUEUE_SERIAL);return self;}
- (void)start:(AVAudioMixerNode *)mixer {
 if(_started)return;_path=NSProcessInfo.processInfo.environment[@"PORTICO_AUDIO_OUTPUT_CAPTURE"];if(!_path.length||![_path hasPrefix:@"/"])return;
 _started=YES;AVAudioFormat *format=[mixer outputFormatForBus:0];if(format.channelCount<1||format.sampleRate<=0){_error=@"No output format.";return;}
 _sampleRate=format.sampleRate;_channels=format.channelCount;NSError *error=nil;
 _file=[[AVAudioFile alloc]initForWriting:[NSURL fileURLWithPath:_path] settings:format.settings commonFormat:AVAudioPCMFormatFloat32 interleaved:NO error:&error];
 if(!_file){_error=error.localizedDescription;return;}_mixer=mixer;
 __weak PorticoAudioCapture *weakSelf=self;
 [mixer installTapOnBus:0 bufferSize:4096 format:format block:^(AVAudioPCMBuffer *buffer,AVAudioTime *when){
  PorticoAudioCapture *s=weakSelf;if(!s||atomic_load(&s->_failed))return;
  if(atomic_fetch_add(&s->_pending,1)>=8){atomic_fetch_sub(&s->_pending,1);atomic_store(&s->_failed,true);return;}
  if((atomic_load(&s->_frames)+buffer.frameLength)*(long long)s->_channels*4>64LL*1024*1024){atomic_fetch_sub(&s->_pending,1);atomic_store(&s->_failed,true);return;}
  AVAudioPCMBuffer *copy=[[AVAudioPCMBuffer alloc]initWithPCMFormat:buffer.format frameCapacity:buffer.frameLength];copy.frameLength=buffer.frameLength;
  for(AVAudioChannelCount ch=0;ch<buffer.format.channelCount;ch++)memcpy(copy.floatChannelData[ch],buffer.floatChannelData[ch],buffer.frameLength*sizeof(float));
  dispatch_async(s->_writer,^{NSError *failure=nil;if(![s->_file writeFromBuffer:copy error:&failure]){s->_error=failure.localizedDescription;atomic_store(&s->_failed,true);}else atomic_fetch_add(&s->_frames,copy.frameLength);atomic_fetch_sub(&s->_pending,1);});
 }];
}
- (NSDictionary *)snapshot {return @{@"requested":@(_started),@"frames":@(atomic_load(&_frames)),@"sampleRate":@(_sampleRate),@"channels":@(_channels),@"failed":@(atomic_load(&_failed)||_error.length>0),@"error":_error?:@"",@"boundary":@"AVAudioEngine main mixer output; physical output unverified"};}
- (void)stop {
 if(_mixer){[_mixer removeTapOnBus:0];_mixer=nil;}
 if(!_started)return;_started=NO;dispatch_async(_writer,^{self->_file=nil;NSDictionary *report=[self snapshot];NSData *data=[NSJSONSerialization dataWithJSONObject:report options:NSJSONWritingPrettyPrinted error:nil];[data writeToFile:[self->_path stringByAppendingString:@".json"] options:NSDataWritingAtomic error:nil];});
}
- (void)dealloc {if(_mixer)[_mixer removeTapOnBus:0];}
@end
#endif

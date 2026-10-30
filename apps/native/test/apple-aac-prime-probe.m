#import <Foundation/Foundation.h>
#import <AudioToolbox/AudioToolbox.h>

// Read the actual converter's property support before changing decoder trim policy. No
// audio session, output device, engine, network or credential store participates in this probe.
int main(int argc,const char **argv){@autoreleasepool{
 if(argc<2)return 2;NSMutableArray *rows=[NSMutableArray new];BOOL opened=YES;
 for(int i=1;i<argc;i++){
  NSURL *url=[NSURL fileURLWithPath:[NSString stringWithUTF8String:argv[i]]];AudioFileID file=NULL;
  OSStatus status=AudioFileOpenURL((__bridge CFURLRef)url,kAudioFileReadPermission,0,&file);
  NSMutableDictionary *row=[@{@"file":url.lastPathComponent,@"openStatus":@(status)}mutableCopy];
  if(status!=noErr){opened=NO;[rows addObject:row];continue;}
  AudioStreamBasicDescription source={0};UInt32 size=sizeof(source);status=AudioFileGetProperty(file,kAudioFilePropertyDataFormat,&size,&source);row[@"formatStatus"]=@(status);
  AudioStreamBasicDescription output={.mSampleRate=source.mSampleRate,.mFormatID=kAudioFormatLinearPCM,.mFormatFlags=kAudioFormatFlagsNativeFloatPacked|kAudioFormatFlagIsNonInterleaved,.mBytesPerPacket=4,.mFramesPerPacket=1,.mBytesPerFrame=4,.mChannelsPerFrame=source.mChannelsPerFrame,.mBitsPerChannel=32};
  AudioConverterRef converter=NULL;if(status==noErr)status=AudioConverterNew(&source,&output,&converter);row[@"converterStatus"]=@(status);
  if(status==noErr){
   row[@"sourceRate"]=@(source.mSampleRate);row[@"sourceFramesPerPacket"]=@(source.mFramesPerPacket);row[@"outputRate"]=@(output.mSampleRate);
   UInt32 propertySize=0;Boolean writable=false;OSStatus info=AudioConverterGetPropertyInfo(converter,kAudioConverterPrimeMethod,&propertySize,&writable);
   row[@"primeMethodInfoStatus"]=@(info);row[@"primeMethodSize"]=@(propertySize);row[@"primeMethodWritable"]=@(writable);
   UInt32 method=UINT32_MAX;size=sizeof(method);row[@"primeMethodReadStatus"]=@(AudioConverterGetProperty(converter,kAudioConverterPrimeMethod,&size,&method));row[@"primeMethodBefore"]=@(method);
   UInt32 none=kConverterPrimeMethod_None;row[@"primeMethodSetNoneStatus"]=@(AudioConverterSetProperty(converter,kAudioConverterPrimeMethod,sizeof(none),&none));
   // Distinguish an unavailable property from a decoder that first needs its real
   // AudioSpecificConfig. This is the exact file cookie, without synthesized defaults.
   UInt32 cookieSize=0,cookieWritable=0;OSStatus cookieInfo=AudioFileGetPropertyInfo(file,kAudioFilePropertyMagicCookieData,&cookieSize,&cookieWritable);row[@"cookieInfoStatus"]=@(cookieInfo);row[@"cookieBytes"]=@(cookieSize);
   if(cookieInfo==noErr&&cookieSize>0&&cookieSize<=65536){
    NSMutableData *cookie=[NSMutableData dataWithLength:cookieSize];OSStatus read=AudioFileGetProperty(file,kAudioFilePropertyMagicCookieData,&cookieSize,cookie.mutableBytes);row[@"cookieReadStatus"]=@(read);
    if(read==noErr){row[@"cookieSetStatus"]=@(AudioConverterSetProperty(converter,kAudioConverterDecompressionMagicCookie,cookieSize,cookie.bytes));
     propertySize=0;writable=false;row[@"primeMethodInfoAfterCookieStatus"]=@(AudioConverterGetPropertyInfo(converter,kAudioConverterPrimeMethod,&propertySize,&writable));row[@"primeMethodAfterCookieSize"]=@(propertySize);row[@"primeMethodAfterCookieWritable"]=@(writable);
     row[@"primeMethodSetNoneAfterCookieStatus"]=@(AudioConverterSetProperty(converter,kAudioConverterPrimeMethod,sizeof(none),&none));
    }
   }
   AudioConverterPrimeInfo prime={0};size=sizeof(prime);row[@"primeInfoStatus"]=@(AudioConverterGetProperty(converter,kAudioConverterPrimeInfo,&size,&prime));row[@"leadingFrames"]=@(prime.leadingFrames);row[@"trailingFrames"]=@(prime.trailingFrames);
   AudioFilePacketTableInfo table={0};size=sizeof(table);row[@"packetTableStatus"]=@(AudioFileGetProperty(file,kAudioFilePropertyPacketTableInfo,&size,&table));row[@"filePrimingFrames"]=@(table.mPrimingFrames);row[@"fileValidFrames"]=@(table.mNumberValidFrames);row[@"fileRemainderFrames"]=@(table.mRemainderFrames);
   AudioConverterDispose(converter);
  }else opened=NO;
  AudioFileClose(file);[rows addObject:row];
 }
 NSData *json=[NSJSONSerialization dataWithJSONObject:@{@"convertersOpened":@(opened),@"observations":rows}options:NSJSONWritingPrettyPrinted|NSJSONWritingSortedKeys error:nil];puts([[NSString alloc]initWithData:json encoding:NSUTF8StringEncoding].UTF8String);return opened?0:1;
}}

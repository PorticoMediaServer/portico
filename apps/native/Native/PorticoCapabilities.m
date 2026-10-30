#import <React/RCTBridgeModule.h>
#import <React/RCTEventEmitter.h>
#import <AVFoundation/AVFoundation.h>
#import <VideoToolbox/VideoToolbox.h>
#import <AudioToolbox/AudioToolbox.h>
#import <UIKit/UIKit.h>
#import <sys/utsname.h>

/** Answers what only the system knows about this device right now: which HDR formats the panel
 * (or the television behind an Apple TV) accepts, which codecs the chip decodes in hardware, and
 * what the current audio output carries. The app turns the answers into the capability profile
 * its server plans delivery against. Everything here is a read: the audio session is never
 * activated, so asking cannot interrupt anyone's audio. */
@interface PorticoCapabilities : RCTEventEmitter <RCTBridgeModule> @end
@implementation PorticoCapabilities { BOOL _listening; }
RCT_EXPORT_MODULE()
+ (BOOL)requiresMainQueueSetup { return NO; }
- (NSArray<NSString *> *)supportedEvents { return @[@"portico.capabilities"]; }

- (void)startObserving {
 _listening = YES;
 NSNotificationCenter *center = NSNotificationCenter.defaultCenter;
 [center addObserver:self selector:@selector(changed) name:AVAudioSessionRouteChangeNotification object:nil];
 [center addObserver:self selector:@selector(changed) name:AVPlayerEligibleForHDRPlaybackDidChangeNotification object:nil];
 if (@available(iOS 15.0, tvOS 15.0, *)) {
  [center addObserver:self selector:@selector(changed) name:AVAudioSessionSpatialPlaybackCapabilitiesChangedNotification object:nil];
 }
}
- (void)stopObserving { _listening = NO; [NSNotificationCenter.defaultCenter removeObserver:self]; }
- (void)invalidate { [self stopObserving]; [super invalidate]; }
- (void)changed { if (_listening) [self sendEventWithName:@"portico.capabilities" body:@{}]; }

/* Converter creation checks the decoder at each named rate. This is not a USB DAC rate
 * ceiling: iOS exposes the current accepted output rate, not the hardware's full rate list. */
- (NSArray *)audioDecoders:(NSInteger)outputChannels {
 NSInteger channels=MAX(1,MIN(8,outputChannels>0?outputChannels:2));
 static NSMutableDictionary *cache;static dispatch_once_t once;dispatch_once(&once,^{cache=[NSMutableDictionary new];});
 if(cache[@(channels)])return cache[@(channels)];
 NSArray *specs=@[@{@"codec":@"flac",@"containers":@[@"flac"],@"id":@(kAudioFormatFLAC)},@{@"codec":@"alac",@"containers":@[@"mp4"],@"id":@(kAudioFormatAppleLossless)},@{@"codec":@"aac",@"containers":@[@"mp4",@"adts"],@"id":@(kAudioFormatMPEG4AAC)},@{@"codec":@"mp3",@"containers":@[@"mp3"],@"id":@(kAudioFormatMPEGLayer3)},@{@"codec":@"pcm",@"containers":@[@"wav",@"aiff"],@"id":@(kAudioFormatLinearPCM)}];
 NSMutableArray *result=[NSMutableArray new];NSArray *candidates=@[@8000,@11025,@12000,@16000,@22050,@24000,@32000,@44100,@48000,@64000,@88200,@96000,@128000,@176400,@192000,@256000,@352800,@384000,@705600,@768000];
 for(NSDictionary *spec in specs){
  NSMutableArray *rates=[NSMutableArray new];NSInteger maxChannels=0;
  for(NSNumber *rate in candidates){
   AudioStreamBasicDescription input={0};input.mFormatID=[spec[@"id"] unsignedIntValue];input.mSampleRate=rate.doubleValue;input.mChannelsPerFrame=(UInt32)channels;
   if(input.mFormatID==kAudioFormatLinearPCM){input.mFormatFlags=kAudioFormatFlagIsSignedInteger|kAudioFormatFlagIsPacked;input.mBitsPerChannel=24;input.mBytesPerFrame=(UInt32)channels*3;input.mFramesPerPacket=1;input.mBytesPerPacket=input.mBytesPerFrame;}
   else {UInt32 size=sizeof(input);if(AudioFormatGetProperty(kAudioFormatProperty_FormatInfo,0,NULL,&size,&input)!=noErr)continue;}
   AudioStreamBasicDescription output={0};output.mFormatID=kAudioFormatLinearPCM;output.mSampleRate=rate.doubleValue;output.mChannelsPerFrame=(UInt32)channels;output.mFormatFlags=kAudioFormatFlagsNativeFloatPacked|kAudioFormatFlagIsNonInterleaved;output.mBitsPerChannel=32;output.mBytesPerFrame=4;output.mFramesPerPacket=1;output.mBytesPerPacket=4;
   AudioConverterRef converter=NULL;if(AudioConverterNew(&input,&output,&converter)==noErr&&converter){[rates addObject:rate];maxChannels=channels;AudioConverterDispose(converter);}
  }
  if(rates.count)[result addObject:@{@"codec":spec[@"codec"],@"containers":spec[@"containers"],@"sampleRates":rates,@"maxSampleRate":rates.lastObject,@"maxChannels":@(maxChannels),@"maxBitDepth":@24,@"via":@"AudioConverter+ExtAudioFile"}];
 }
 [result addObject:@{@"codec":@"opus",@"containers":@[@"ogg"],@"sampleRates":@[@48000],@"maxSampleRate":@48000,@"maxChannels":@(channels),@"maxBitDepth":@24,@"via":@"FFmpeg Opus raw-trim"}];
 cache[@(channels)]=[result copy];return result;
}

RCT_REMAP_METHOD(facts, factsWithResolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject) {
 dispatch_async(dispatch_get_main_queue(), ^{
  struct utsname system; uname(&system);
  NSString *model = [NSString stringWithCString:system.machine encoding:NSUTF8StringEncoding] ?: @"";
#if TARGET_OS_SIMULATOR
  model = NSProcessInfo.processInfo.environment[@"SIMULATOR_MODEL_IDENTIFIER"] ?: model;
#endif
  // On Apple TV these follow the HDMI sink and the viewer's video settings.
  AVPlayerHDRMode modes = AVPlayer.availableHDRModes;
  UIScreen *screen = UIScreen.mainScreen;
  CGSize pixels = screen.nativeBounds.size;
  AVAudioSession *audio = AVAudioSession.sharedInstance;
#if TARGET_OS_TV
  // COMPAT-03: without this the session reports 2 channels on a 5.1/7.1 HDMI receiver, so the
  // server converted surround to stereo AAC. It must be set before the channels are read.
  if (@available(tvOS 15.0, *)) [audio setSupportsMultichannelContent:YES error:nil];
#endif
  AVAudioSessionPortDescription *port = audio.currentRoute.outputs.firstObject;
  BOOL spatial = NO, av1 = NO;
  if (@available(iOS 15.0, tvOS 15.0, *)) spatial = port.isSpatialAudioEnabled;
  if (@available(iOS 17.0, tvOS 17.0, *)) av1 = VTIsHardwareDecodeSupported(kCMVideoCodecType_AV1);
  BOOL dv=VTIsHardwareDecodeSupported(kCMVideoCodecType_DolbyVisionHEVC);
  BOOL p5=dv&&[AVURLAsset isPlayableExtendedMIMEType:@"video/mp4; codecs=\"dvh1.05.06\""];
  BOOL p8=dv&&[AVURLAsset isPlayableExtendedMIMEType:@"video/mp4; codecs=\"dvh1.08.06\""];
  BOOL joc=spatial&&[AVURLAsset isPlayableExtendedMIMEType:@"audio/mp4; codecs=\"ec-3\""];
  resolve(@{
   @"model": model,
   @"osVersion": UIDevice.currentDevice.systemVersion ?: @"",
   // APL-AUTH-06: the name the owner gave this device ("Living Room") for sign-in approvals, and
   // the app's own version. On iOS 16+ the system may answer a generic name; the app falls back.
   @"deviceName": UIDevice.currentDevice.name ?: @"",
   @"appVersion": [NSBundle.mainBundle objectForInfoDictionaryKey:@"CFBundleShortVersionString"] ?: @"",
   @"hardwareDecode": @{@"hevc": @(VTIsHardwareDecodeSupported(kCMVideoCodecType_HEVC)), @"av1": @(av1), @"dolbyVision": @(VTIsHardwareDecodeSupported(kCMVideoCodecType_DolbyVisionHEVC))},
   @"hdrModes": @{@"hdr10": @((modes & AVPlayerHDRModeHDR10) != 0), @"hlg": @((modes & AVPlayerHDRModeHLG) != 0), @"dolbyVision": @((modes & AVPlayerHDRModeDolbyVision) != 0)},
   @"videoEligibility":@{@"dolbyVision5":@(p5),@"dolbyVision81":@(p8&&((modes&AVPlayerHDRModeHDR10)!=0)),@"dolbyVision84":@(p8&&((modes&AVPlayerHDRModeHLG)!=0))},
   @"audioDecode":[self audioDecoders:audio.maximumOutputNumberOfChannels],
   @"subtitleRendering":@[@"text",@"ass",@"pgs",@"vobsub",@"dvb"],
   @"eligibleForHDRPlayback": @(AVPlayer.eligibleForHDRPlayback),
   @"screen": @{@"width": @(MAX(pixels.width, pixels.height)), @"height": @(MIN(pixels.width, pixels.height)), @"maxFrameRate": @(screen.maximumFramesPerSecond)},
   @"audio": @{@"maxOutputChannels": @(MAX(audio.maximumOutputNumberOfChannels, (NSInteger)port.channels.count)), @"route": [self route:port.portType], @"spatial": @(spatial),@"currentOutputSampleRate":@(audio.sampleRate),@"outputNumberOfChannels":@(audio.outputNumberOfChannels),@"maximumOutputNumberOfChannels":@(audio.maximumOutputNumberOfChannels),@"routeUID":port.UID?:@"",@"eac3JocEligible":@(joc)},
  });
 });
}

/** The port type as one of the words the profile uses. */
- (NSString *)route:(AVAudioSessionPort)port {
 if (!port) return @"";
 if ([port isEqualToString:AVAudioSessionPortHDMI]) return @"hdmi";
 if ([port isEqualToString:AVAudioSessionPortAirPlay]) return @"airplay";
 if ([port isEqualToString:AVAudioSessionPortBluetoothA2DP] || [port isEqualToString:AVAudioSessionPortBluetoothLE] || [port isEqualToString:AVAudioSessionPortBluetoothHFP]) return @"bluetooth";
 if ([port isEqualToString:AVAudioSessionPortHeadphones]) return @"headphones";
 if ([port isEqualToString:AVAudioSessionPortUSBAudio]) return @"usb";
 if ([port isEqualToString:AVAudioSessionPortBuiltInSpeaker] || [port isEqualToString:AVAudioSessionPortBuiltInReceiver]) return @"speaker";
 return @"other";
}
@end

#import "../Native/PorticoPlaybackObservation.h"

int main(void){@autoreleasepool{
 if(![PorticoObservedDecodedVideo(NULL) isEqual:@{}])return 1;
 CVPixelBufferRef frame=NULL;
 CVReturn status=CVPixelBufferCreate(kCFAllocatorDefault,32,16,kCVPixelFormatType_32BGRA,NULL,&frame);
 if(status!=kCVReturnSuccess||!frame)return 2;
 NSDictionary *observed=PorticoObservedDecodedVideo(frame);
 CVPixelBufferRelease(frame);
 if(![observed isEqual:@{@"video":@{@"width":@32,@"height":@16}}])return 3;
 // Creating an 8-bit analysis buffer does not prove an 8-bit source or SDR output.
 if(observed[@"screen"]||observed[@"audio"]||observed[@"subtitles"]||observed[@"video"][@"bitDepth"]||observed[@"video"][@"dynamicRange"]||observed[@"video"][@"frameRateHz"])return 4;
 puts("Apple decoded-video observation: 3 checks passed");
 return 0;
}}

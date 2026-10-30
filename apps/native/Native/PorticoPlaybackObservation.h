#import <Foundation/Foundation.h>
#import <CoreVideo/CoreVideo.h>

/** Dimensions of an actual decoded buffer. This BGRA analysis outlet does not identify
 * the video's original bit depth, dynamic range, frame rate or the screen's output mode. */
static NSDictionary *PorticoObservedDecodedVideo(CVPixelBufferRef frame) {
 if(!frame)return @{};
 size_t width=CVPixelBufferGetWidth(frame),height=CVPixelBufferGetHeight(frame);
 if(!width||!height||width>32768||height>32768)return @{};
 return @{@"video":@{@"width":@(width),@"height":@(height)}};
}

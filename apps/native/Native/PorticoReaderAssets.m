#import <React/RCTBridgeModule.h>
#import "PorticoReaderAssetOrigin.h"

@interface PorticoReaderAssets : NSObject <RCTBridgeModule> @end
@implementation PorticoReaderAssets
RCT_EXPORT_MODULE()
+ (BOOL)requiresMainQueueSetup{return NO;}
RCT_REMAP_METHOD(bundle,bundleWithResolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject){
 NSURL *entry=PorticoReaderEntryURL();
 if(entry)resolve(@{@"url":entry.absoluteString});
 else reject(@"reader_assets_unavailable",@"reader_assets_unavailable",nil);
}
@end

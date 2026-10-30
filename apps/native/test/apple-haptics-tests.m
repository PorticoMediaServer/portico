#import <Foundation/Foundation.h>
#import <React/RCTBridgeModule.h>
#import <TargetConditionals.h>
#if !TARGET_OS_SIMULATOR
#error This conformance check covers simulator refusal only; physical feedback is a separate check.
#endif
@interface NSObject (PorticoHapticsTest)
- (void)contextMenu:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject;
- (void)selection:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject;
@end
int main(void){@autoreleasepool{
 id module=[NSClassFromString(@"PorticoHaptics")new];__block BOOL context=YES,selection=YES,rejected=NO;__block NSUInteger count=0;
 RCTPromiseRejectBlock reject=^(NSString *code,NSString *message,NSError *error){rejected=YES;};
 [module contextMenu:^(id value){context=[value boolValue];count++;} rejecter:reject];
 [module selection:^(id value){selection=[value boolValue];count++;} rejecter:reject];
 BOOL ok=module&&count==2&&!context&&!selection&&!rejected;
 printf("{\"simulatorContextMenuAndSelectionRefuseHardwarePass\":%s}\n",ok?"true":"false");return ok?0:1;
}}

#import <Foundation/Foundation.h>
#import "../Native/PorticoInstallationStorage.h"

int main(void) {
 @autoreleasepool {
  NSString *suite=[@"portico-storage-test-" stringByAppendingString:NSUUID.UUID.UUIDString];
  NSUserDefaults *defaults=[[NSUserDefaults alloc] initWithSuiteName:suite];
  [defaults setBool:YES forKey:@"PorticoSelectedViewerQuarantined"];
  NSMutableArray *removed=[NSMutableArray array];
  NSCAssert(PorticoPrepareInstallation(defaults,NO,^OSStatus(NSString *service){NSCAssert(NO,@"A background launch before unlock must never delete anything");return errSecSuccess;})==errSecInteractionNotAllowed,@"Protected data must be available before checking the marker");
  OSStatus failed=PorticoPrepareInstallation(defaults,YES,^OSStatus(NSString *service){
   [removed addObject:service];return removed.count==2?errSecInteractionNotAllowed:errSecSuccess;
  });
  NSCAssert(failed==errSecInteractionNotAllowed,@"A locked Keychain must not be accepted");
  NSCAssert(![defaults boolForKey:@"PorticoInstallationPrepared"],@"Failure must remain retryable");
  NSCAssert([defaults boolForKey:@"PorticoSelectedViewerQuarantined"],@"Failure must keep quarantine");
  [removed removeAllObjects];
  NSCAssert(PorticoPrepareInstallation(defaults,YES,^OSStatus(NSString *service){[removed addObject:service];return errSecItemNotFound;})==errSecSuccess,@"Missing records are clean");
  NSSet *expected=[NSSet setWithArray:@[@"tv.getportico.portico.queues",@"tv.getportico.portico.credentials",@"tv.getportico.portico.playback",@"tv.getportico.portico.network",@"tv.getportico.portico.profile-trust",@"tv.getportico.portico.account-continuation"]];
  NSCAssert([[NSSet setWithArray:removed] isEqual:expected],@"Only the exact app services may be removed");
  NSCAssert([defaults boolForKey:@"PorticoInstallationPrepared"]&&! [defaults boolForKey:@"PorticoSelectedViewerQuarantined"],@"Success must persist its marker and clear stale quarantine");
  NSCAssert(PorticoPrepareInstallation(defaults,YES,^OSStatus(NSString *service){NSCAssert(NO,@"An update must retain the installation");return errSecSuccess;})==errSecSuccess,@"An existing installation is retained");
  NSCAssert(PorticoPrepareInstallation(defaults,NO,^OSStatus(NSString *service){NSCAssert(NO,@"Locking an established installation must not wipe it");return errSecSuccess;})==errSecSuccess,@"Prepared background access is retained while locked");
  [defaults removePersistentDomainForName:suite];
 }
 return 0;
}

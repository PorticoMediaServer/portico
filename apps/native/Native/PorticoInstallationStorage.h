#import <Foundation/Foundation.h>
#import <Security/Security.h>

/** A removed installation cannot inherit the previous installation's Keychain credentials.
 * A temporary failure leaves the marker absent: no reader may release old credentials. */
static inline OSStatus PorticoPrepareInstallation(NSUserDefaults *defaults, BOOL protectedDataAvailable, OSStatus (^removeService)(NSString *)) {
 if([defaults boolForKey:@"PorticoInstallationPrepared"])return errSecSuccess;
 if(!protectedDataAvailable)return errSecInteractionNotAllowed;
 for(NSString *suffix in @[@"queues",@"credentials",@"playback",@"network",@"profile-trust",@"account-continuation"]){
  OSStatus status=removeService([@"tv.getportico.portico." stringByAppendingString:suffix]);
  if(status!=errSecSuccess&&status!=errSecItemNotFound)return status;
 }
 [defaults removeObjectForKey:@"PorticoSelectedViewerQuarantined"];
 [defaults setBool:YES forKey:@"PorticoInstallationPrepared"];
 if(![defaults synchronize]){[defaults removeObjectForKey:@"PorticoInstallationPrepared"];return errSecNotAvailable;}
 return errSecSuccess;
}

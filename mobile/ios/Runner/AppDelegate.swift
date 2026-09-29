import AudioToolbox
import FirebaseCore
import FirebaseMessaging
import Flutter
import UIKit
import UserNotifications

@main
@objc class AppDelegate: FlutterAppDelegate, FlutterImplicitEngineDelegate {
  /// Push notifications (#379): the permission, the device token for the
  /// instance, and the thread a tapped notification opens. The token is
  /// Firebase Messaging's (#431): the instance sends through FCM, which
  /// hands the notification to Apple with the APNs key of the app's Firebase
  /// project.
  private var push: FlutterMethodChannel?
  private var pendingToken: FlutterResult?
  /// Whether this build carries the app's Firebase project. Without its
  /// GoogleService-Info.plist, which whoever ships the app puts in, there is
  /// no push.
  private var firebase = false
  /// The FCM token last handed to Dart; a different one later is a refresh.
  private var handedOut: String?
  /// The agent of a notification tapped before Dart was listening — the
  /// app was started by the tap.
  private var launchAgent: String?

  override func application(
    _ application: UIApplication,
    didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]?
  ) -> Bool {
    UNUserNotificationCenter.current().delegate = self
    if Bundle.main.path(forResource: "GoogleService-Info", ofType: "plist") != nil {
      FirebaseApp.configure()
      Messaging.messaging().delegate = self
      firebase = true
    }
    return super.application(application, didFinishLaunchingWithOptions: launchOptions)
  }

  func didInitializeImplicitFlutterEngine(_ engineBridge: FlutterImplicitEngineBridge) {
    GeneratedPluginRegistrant.register(with: engineBridge.pluginRegistry)
    guard let registrar = engineBridge.pluginRegistry.registrar(forPlugin: "CoveyPush") else { return }
    let channel = FlutterMethodChannel(name: "covey/push", binaryMessenger: registrar.messenger())
    channel.setMethodCallHandler { [weak self] call, result in
      self?.handle(call, result)
    }
    push = channel
  }

  private func handle(_ call: FlutterMethodCall, _ result: @escaping FlutterResult) {
    switch call.method {
    case "register":
      guard firebase else {
        return result(FlutterError(code: "unavailable", message: "this build has no Firebase configuration", details: nil))
      }
      // Asks once; afterwards the answer stands and only the Settings app
      // changes it.
      UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .badge, .sound]) { granted, _ in
        DispatchQueue.main.async {
          guard granted else { return result(FlutterError(code: "denied", message: nil, details: nil)) }
          self.pendingToken?(FlutterError(code: "superseded", message: nil, details: nil))
          self.pendingToken = result
          UIApplication.shared.registerForRemoteNotifications()
        }
      }
    case "launchAgent":
      result(launchAgent)
      launchAgent = nil
    case "preview":
      // Plays a sound as the settings offer it (#381).
      let name = (call.arguments as? String ?? "").replacingOccurrences(of: ".caf", with: "")
      if name == "default" {
        AudioServicesPlayAlertSound(1007)
      } else if let url = Bundle.main.url(forResource: name, withExtension: "caf") {
        var id: SystemSoundID = 0
        AudioServicesCreateSystemSoundID(url as CFURL, &id)
        AudioServicesPlayAlertSoundWithCompletion(id) { AudioServicesDisposeSystemSoundID(id) }
      }
      result(nil)
    case "badge":
      let n = call.arguments as? Int ?? 0
      UNUserNotificationCenter.current().setBadgeCount(n) { _ in }
      result(nil)
    default:
      result(FlutterMethodNotImplemented)
    }
  }

  override func application(
    _ application: UIApplication, didRegisterForRemoteNotificationsWithDeviceToken deviceToken: Data
  ) {
    super.application(application, didRegisterForRemoteNotificationsWithDeviceToken: deviceToken)
    guard firebase else { return }
    // Told by hand, as swizzling is off (Info.plist); the FCM token that
    // comes back is what the instance keeps.
    Messaging.messaging().apnsToken = deviceToken
    Messaging.messaging().token { token, error in
      DispatchQueue.main.async {
        if let token {
          self.handedOut = token
          self.pendingToken?(token)
        } else {
          self.pendingToken?(FlutterError(code: "failed", message: error?.localizedDescription, details: nil))
        }
        self.pendingToken = nil
      }
    }
  }

  override func application(
    _ application: UIApplication, didFailToRegisterForRemoteNotificationsWithError error: Error
  ) {
    pendingToken?(FlutterError(code: "failed", message: error.localizedDescription, details: nil))
    pendingToken = nil
    super.application(application, didFailToRegisterForRemoteNotificationsWithError: error)
  }

  // In front, a notification still shows: the thread it is about may not be
  // the one on the screen.
  override func userNotificationCenter(
    _ center: UNUserNotificationCenter, willPresent notification: UNNotification,
    withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void
  ) {
    completionHandler([.banner, .list, .sound, .badge])
  }

  override func userNotificationCenter(
    _ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse,
    withCompletionHandler completionHandler: @escaping () -> Void
  ) {
    if let agent = response.notification.request.content.userInfo["agent_id"] as? String {
      if let push {
        push.invokeMethod("open", arguments: agent)
      } else {
        launchAgent = agent
      }
    }
    completionHandler()
  }
}

extension AppDelegate: MessagingDelegate {
  /// Firebase replaced the token: Dart registers again, and the instance
  /// forgets the old one when it is refused. The first token after a start
  /// is the one register answers with, and is not news.
  func messaging(_ messaging: Messaging, didReceiveRegistrationToken fcmToken: String?) {
    guard let fcmToken, let handedOut, fcmToken != handedOut else { return }
    DispatchQueue.main.async {
      self.handedOut = fcmToken
      self.push?.invokeMethod("token", arguments: fcmToken)
    }
  }
}

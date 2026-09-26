import AudioToolbox
import Flutter
import UIKit
import UserNotifications

@main
@objc class AppDelegate: FlutterAppDelegate, FlutterImplicitEngineDelegate {
  /// Push notifications (#379): the permission, the device token for the
  /// instance, and the thread a tapped notification opens.
  private var push: FlutterMethodChannel?
  private var pendingToken: FlutterResult?
  /// The agent of a notification tapped before Dart was listening — the
  /// app was started by the tap.
  private var launchAgent: String?

  override func application(
    _ application: UIApplication,
    didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]?
  ) -> Bool {
    UNUserNotificationCenter.current().delegate = self
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
    let token = deviceToken.map { String(format: "%02x", $0) }.joined()
    pendingToken?(token)
    pendingToken = nil
    super.application(application, didRegisterForRemoteNotificationsWithDeviceToken: deviceToken)
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

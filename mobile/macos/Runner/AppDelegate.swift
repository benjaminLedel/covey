import Cocoa
import FlutterMacOS

@main
class AppDelegate: FlutterAppDelegate {
  // Dictate anywhere (#355) answers a global shortcut: the app keeps running
  // when its window is closed, and the Dock icon brings the window back.
  override func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
    return false
  }

  override func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
    if !flag {
      for window in sender.windows where window is MainFlutterWindow {
        window.makeKeyAndOrderFront(self)
      }
    }
    return true
  }

  override func applicationSupportsSecureRestorableState(_ app: NSApplication) -> Bool {
    return true
  }
}

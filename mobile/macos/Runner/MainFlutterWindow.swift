import AVFoundation
import ApplicationServices
import Cocoa
import CoreAudio
import FlutterMacOS
import UserNotifications

class MainFlutterWindow: NSWindow {
  private var flow: FlowBridge?
  private var chrome: FlutterMethodChannel?
  private var systemAudio: FlutterEventChannel?
  private let systemAudioHandler = SystemAudioHandler()
  private var camera: FlutterMethodChannel?
  private let cameraSheet = CameraSheet()
  private var notices: LocalNotices?

  override func awakeFromNib() {
    let flutterViewController = FlutterViewController()
    let windowFrame = self.frame
    self.contentViewController = flutterViewController
    self.setFrame(windowFrame, display: true)
    // Closing the window hides it: the app keeps answering the dictation
    // shortcut (#355), and the Dock icon brings it back.
    self.isReleasedWhenClosed = false

    // One unified bar (#356): the content runs under a transparent title
    // bar, and an empty toolbar gives it the height of Finder's or Mail's,
    // so the traffic lights sit in the app's own top bar.
    self.styleMask.insert(.fullSizeContentView)
    self.titlebarAppearsTransparent = true
    self.titleVisibility = .hidden
    let toolbar = NSToolbar(identifier: "covey")
    toolbar.showsBaselineSeparator = false
    self.toolbar = toolbar
    self.toolbarStyle = .unified

    RegisterGeneratedPlugins(registry: flutterViewController)
    flow = FlowBridge(messenger: flutterViewController.engine.binaryMessenger)
    systemAudio = FlutterEventChannel(name: "covey/system-audio", binaryMessenger: flutterViewController.engine.binaryMessenger)
    systemAudio?.setStreamHandler(systemAudioHandler)
    chrome = FlutterMethodChannel(name: "covey/window", binaryMessenger: flutterViewController.engine.binaryMessenger)
    chrome?.setMethodCallHandler { [weak self] call, result in
      guard let self else { return result(nil) }
      switch call.method {
      case "metrics":
        // The bar's height and where the traffic lights end, in the
        // Flutter view's coordinates (points from the top left).
        let bar = self.frame.height - self.contentLayoutRect.height
        let zoom = self.standardWindowButton(.zoomButton)?.frame
        result(["height": Double(bar), "lightsRight": Double((zoom?.maxX ?? 70) + 12)])
      case "drag":
        // The Flutter view takes every click, the title bar's included:
        // the app's empty bar areas move the window instead.
        if let event = NSApp.currentEvent { self.performDrag(with: event) }
        result(nil)
      case "zoom":
        self.performZoom(nil)
        result(nil)
      case "activate":
        NSApp.activate(ignoringOtherApps: true)
        self.makeKeyAndOrderFront(nil)
        result(nil)
      default:
        result(FlutterMethodNotImplemented)
      }
    }

    // Notifications (#379): the Mac app runs anyway, so it shows local ones
    // itself rather than through Apple's push service.
    notices = LocalNotices(messenger: flutterViewController.engine.binaryMessenger)

    // The profile photo (#377): the Mac has no system camera screen to
    // borrow, so the app opens a small camera sheet of its own.
    camera = FlutterMethodChannel(name: "covey/camera", binaryMessenger: flutterViewController.engine.binaryMessenger)
    camera?.setMethodCallHandler { [weak self] call, result in
      guard let self, call.method == "capture" else { return result(FlutterMethodNotImplemented) }
      let args = call.arguments as? [String: String] ?? [:]
      self.cameraSheet.capture(over: self, take: args["take"] ?? "Take", cancel: args["cancel"] ?? "Cancel", result: result)
    }

    super.awakeFromNib()
  }
}

/// Local notifications on the Mac (#379): Dart says what is new, this
/// shows it, and a click opens the thread.
final class LocalNotices: NSObject, UNUserNotificationCenterDelegate {
  private let channel: FlutterMethodChannel
  /// The sound a preview plays. Held here: an NSSound nobody holds is
  /// freed at once and falls silent before it is heard.
  private var playing: NSSound?

  init(messenger: FlutterBinaryMessenger) {
    channel = FlutterMethodChannel(name: "covey/push", binaryMessenger: messenger)
    super.init()
    UNUserNotificationCenter.current().delegate = self
    channel.setMethodCallHandler { [weak self] call, result in
      self?.handle(call, result)
    }
  }

  private func handle(_ call: FlutterMethodCall, _ result: @escaping FlutterResult) {
    switch call.method {
    case "authorize":
      // Answers what the system allows, for the diagnostic log: whether
      // notifications show at all, and whether they may make a sound.
      UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound, .badge]) { granted, _ in
        UNUserNotificationCenter.current().getNotificationSettings { settings in
          DispatchQueue.main.async {
            result([
              "granted": granted,
              "alert": settings.alertSetting == .enabled,
              "sound": settings.soundSetting == .enabled,
            ])
          }
        }
      }
    case "notify":
      let args = call.arguments as? [String: Any] ?? [:]
      let content = UNMutableNotificationContent()
      content.title = args["title"] as? String ?? ""
      content.body = args["body"] as? String ?? ""
      // The sound the person chose (#381): a file in the bundle, the
      // system's, or none.
      // covey's own sounds the app plays itself: macOS takes a named sound
      // for a local notification without complaint and then often plays
      // nothing. Only while the person allows notification sounds.
      switch args["sound"] as? String ?? "default" {
      case "": content.sound = nil
      case "default": content.sound = .default
      case let name:
        content.sound = nil
        UNUserNotificationCenter.current().getNotificationSettings { settings in
          guard settings.soundSetting == .enabled else { return }
          DispatchQueue.main.async { self.play(name) }
        }
      }
      let agent = args["agent"] as? String ?? ""
      content.threadIdentifier = agent
      content.userInfo = ["agent_id": agent]
      let request = UNNotificationRequest(identifier: args["id"] as? String ?? UUID().uuidString, content: content, trigger: nil)
      UNUserNotificationCenter.current().add(request) { error in
        DispatchQueue.main.async { result(error?.localizedDescription) }
      }
    case "badge":
      let n = call.arguments as? Int ?? 0
      NSApp.dockTile.badgeLabel = n > 0 ? "\(n)" : nil
      result(nil)
    case "launchAgent":
      result(nil)
    case "preview":
      // Plays a sound as the settings offer it.
      let name = call.arguments as? String ?? ""
      if name == "default" {
        NSSound.beep()
      } else {
        play(name)
      }
      result(nil)
    default:
      result(FlutterMethodNotImplemented)
    }
  }

  /// Plays one of the bundle's sounds, holding it until it has played.
  private func play(_ file: String) {
    let name = file.replacingOccurrences(of: ".caf", with: "")
    guard let url = Bundle.main.url(forResource: name, withExtension: "caf") else { return }
    playing?.stop()
    playing = NSSound(contentsOf: url, byReference: true)
    playing?.play()
  }

  func userNotificationCenter(
    _ center: UNUserNotificationCenter, willPresent notification: UNNotification,
    withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void
  ) {
    completionHandler([.banner, .list, .sound])
  }

  func userNotificationCenter(
    _ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse,
    withCompletionHandler completionHandler: @escaping () -> Void
  ) {
    if let agent = response.notification.request.content.userInfo["agent_id"] as? String {
      NSApp.activate(ignoringOtherApps: true)
      NSApp.windows.first { $0 is MainFlutterWindow }?.makeKeyAndOrderFront(nil)
      channel.invokeMethod("open", arguments: agent)
    }
    completionHandler()
  }
}

/// The camera sheet for the profile photo (#377): a mirrored preview, one
/// button that takes the picture, one that cancels. Answers the JPEG, nil
/// when cancelled, or the error "denied" when the camera is not allowed.
final class CameraSheet: NSObject, AVCapturePhotoCaptureDelegate {
  private let session = AVCaptureSession()
  private let output = AVCapturePhotoOutput()
  private var sheet: NSWindow?
  private var parent: NSWindow?
  private var result: FlutterResult?

  func capture(over window: NSWindow, take: String, cancel: String, result: @escaping FlutterResult) {
    guard self.result == nil else { return result(FlutterError(code: "busy", message: nil, details: nil)) }
    switch AVCaptureDevice.authorizationStatus(for: .video) {
    case .authorized:
      open(over: window, take: take, cancel: cancel, result: result)
    case .notDetermined:
      AVCaptureDevice.requestAccess(for: .video) { ok in
        DispatchQueue.main.async {
          if ok {
            self.open(over: window, take: take, cancel: cancel, result: result)
          } else {
            result(FlutterError(code: "denied", message: nil, details: nil))
          }
        }
      }
    default:
      result(FlutterError(code: "denied", message: nil, details: nil))
    }
  }

  private func open(over window: NSWindow, take: String, cancel: String, result: @escaping FlutterResult) {
    guard let device = AVCaptureDevice.default(for: .video),
      let input = try? AVCaptureDeviceInput(device: device)
    else { return result(FlutterError(code: "unavailable", message: "no camera", details: nil)) }
    session.beginConfiguration()
    session.sessionPreset = .photo
    session.inputs.forEach { session.removeInput($0) }
    if session.canAddInput(input) { session.addInput(input) }
    if !session.outputs.contains(output), session.canAddOutput(output) { session.addOutput(output) }
    // The picture as the preview shows it: mirrored, as a mirror would.
    if let conn = output.connection(with: .video), conn.isVideoMirroringSupported {
      conn.automaticallyAdjustsVideoMirroring = false
      conn.isVideoMirrored = true
    }
    session.commitConfiguration()

    let w: CGFloat = 480, h: CGFloat = 360, bar: CGFloat = 56
    let content = NSView(frame: NSRect(x: 0, y: 0, width: w, height: h + bar))
    let preview = NSView(frame: NSRect(x: 0, y: bar, width: w, height: h))
    preview.wantsLayer = true
    let layer = AVCaptureVideoPreviewLayer(session: session)
    layer.videoGravity = .resizeAspectFill
    layer.frame = preview.bounds
    if let conn = layer.connection, conn.isVideoMirroringSupported {
      conn.automaticallyAdjustsVideoMirroring = false
      conn.isVideoMirrored = true
    }
    preview.layer?.addSublayer(layer)
    content.addSubview(preview)

    let shoot = NSButton(title: take, target: self, action: #selector(shoot))
    shoot.bezelStyle = .rounded
    shoot.keyEquivalent = "\r"
    let stop = NSButton(title: cancel, target: self, action: #selector(dismiss))
    stop.bezelStyle = .rounded
    stop.keyEquivalent = "\u{1b}"
    shoot.sizeToFit()
    stop.sizeToFit()
    shoot.frame.origin = NSPoint(x: w - shoot.frame.width - 16, y: (bar - shoot.frame.height) / 2)
    stop.frame.origin = NSPoint(x: shoot.frame.minX - stop.frame.width - 8, y: (bar - stop.frame.height) / 2)
    content.addSubview(shoot)
    content.addSubview(stop)

    let sheet = NSWindow(contentRect: content.frame, styleMask: [.titled], backing: .buffered, defer: false)
    sheet.contentView = content
    self.sheet = sheet
    self.parent = window
    self.result = result
    window.beginSheet(sheet)
    DispatchQueue.global(qos: .userInitiated).async { self.session.startRunning() }
  }

  @objc private func shoot() {
    let settings = AVCapturePhotoSettings(format: [AVVideoCodecKey: AVVideoCodecType.jpeg])
    output.capturePhoto(with: settings, delegate: self)
  }

  @objc private func dismiss() { finish(nil) }

  func photoOutput(_ output: AVCapturePhotoOutput, didFinishProcessingPhoto photo: AVCapturePhoto, error: Error?) {
    let data = photo.fileDataRepresentation()
    DispatchQueue.main.async { self.finish(data) }
  }

  private func finish(_ data: Data?) {
    DispatchQueue.global(qos: .utility).async { self.session.stopRunning() }
    if let sheet { parent?.endSheet(sheet) }
    sheet = nil
    parent = nil
    result?(data.map { FlutterStandardTypedData(bytes: $0) })
    result = nil
  }
}

/// Dictate anywhere (#355), the native half: the floating panel that shows
/// what is heard, the Accessibility permission, and putting the text into
/// the app that has the focus. The shortcut, the recording and the
/// recognition are Dart's.
final class FlowBridge {
  private let channel: FlutterMethodChannel
  private let panel = FlowPanel()

  private lazy var status = ActivityStatusItem { [weak self] action in
    self?.channel.invokeMethod("statusAction", arguments: action)
  }

  init(messenger: FlutterBinaryMessenger) {
    channel = FlutterMethodChannel(name: "covey/flow", binaryMessenger: messenger)
    channel.setMethodCallHandler { [weak self] call, result in
      self?.handle(call, result)
    }
  }

  /// Apps whose content is never read — not for dictation's context, not
  /// for the activity log (#363): password managers and the system's
  /// keychain. Only the fact that one was in front is recorded.
  private static let excluded: Set<String> = [
    "com.apple.Passwords", "com.apple.keychainaccess", "com.1password.1password", "com.agilebits.onepassword7",
    "com.bitwarden.desktop", "com.dashlane.dashlanephonefinal", "com.lastpass.LastPass", "org.keepassxc.keepassxc",
    "com.apple.systempreferences",
  ]

  /// Chromium browsers and Electron apps build their accessibility tree only
  /// when asked to (AXManualAccessibility); without it they show no address
  /// and no field.
  private static let chromium: Set<String> = [
    "com.google.Chrome", "com.microsoft.edgemac", "com.brave.Browser", "company.thebrowser.Browser",
    "com.vivaldi.Vivaldi", "com.tinyspeck.slackmacgap", "com.microsoft.teams2", "com.microsoft.VSCode",
    "com.hnc.Discord", "notion.id", "com.linear",
  ]
  private var enabledTrees: Set<pid_t> = []

  /// One sample for the activity log (#363): what `focus` reads, plus the
  /// app's bundle id, the page's address where there is one, and how long
  /// the person has been idle.
  private func sample() -> [String: Any] {
    let idle = CGEventSource.secondsSinceLastEventType(
      .combinedSessionState, eventType: CGEventType(rawValue: ~0)!)
    guard let app = NSWorkspace.shared.frontmostApplication else { return ["idle": idle] }
    let bundle = app.bundleIdentifier ?? ""
    if Self.excluded.contains(bundle) {
      return ["idle": idle, "app": app.localizedName ?? "", "bundle": bundle, "excluded": true]
    }
    if AXIsProcessTrusted(), Self.chromium.contains(bundle), !enabledTrees.contains(app.processIdentifier) {
      let el = AXUIElementCreateApplication(app.processIdentifier)
      AXUIElementSetAttributeValue(el, "AXManualAccessibility" as CFString, kCFBooleanTrue)
      enabledTrees.insert(app.processIdentifier)
    }
    var out = focus()
    out["idle"] = idle
    out["bundle"] = bundle
    if AXIsProcessTrusted(), let url = pageAddress(app) { out["url"] = url }
    return out
  }

  /// The address of the page in the front window: the first element below
  /// it that has one (a browser's web area), a few levels deep at most.
  private func pageAddress(_ app: NSRunningApplication) -> String? {
    func attr(_ el: AXUIElement, _ name: String) -> AnyObject? {
      var v: AnyObject?
      return AXUIElementCopyAttributeValue(el, name as CFString, &v) == .success ? v : nil
    }
    let appElement = AXUIElementCreateApplication(app.processIdentifier)
    AXUIElementSetMessagingTimeout(appElement, 0.25)
    guard let window = attr(appElement, kAXFocusedWindowAttribute) else { return nil }
    var queue: [(AXUIElement, Int)] = [(window as! AXUIElement, 0)]
    var visited = 0
    while !queue.isEmpty && visited < 400 {
      let (el, depth) = queue.removeFirst()
      visited += 1
      if let url = attr(el, kAXURLAttribute) {
        if let u = url as? URL, u.scheme == "http" || u.scheme == "https" { return u.absoluteString }
        if let s = url as? String, s.hasPrefix("http") { return s }
      }
      if depth < 8, let children = attr(el, kAXChildrenAttribute) as? [AXUIElement] {
        queue.append(contentsOf: children.map { ($0, depth + 1) })
      }
    }
    return nil
  }

  private func handle(_ call: FlutterMethodCall, _ result: @escaping FlutterResult) {
    let args = call.arguments as? [String: Any] ?? [:]
    switch call.method {
    case "trusted":
      result(AXIsProcessTrusted())
    case "askTrust":
      let prompt = kAXTrustedCheckOptionPrompt.takeUnretainedValue() as String
      result(AXIsProcessTrustedWithOptions([prompt: true] as CFDictionary))
    case "openAccessibilitySettings":
      if let url = URL(string: "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility") {
        NSWorkspace.shared.open(url)
      }
      result(nil)
    case "frontmostApp":
      result(NSWorkspace.shared.frontmostApplication?.localizedName)
    case "focus":
      result(focus())
    case "sample":
      result(sample())
    case "timeZone":
      result(TimeZone.current.identifier)
    case "status":
      status.update(args)
      result(nil)
    case "show":
      panel.show()
      result(nil)
    case "update":
      panel.update(
        text: args["text"] as? String ?? "",
        level: (args["levels"] as? [Double])?.last ?? 0,
        busy: args["busy"] as? Bool ?? false)
      result(nil)
    case "hide":
      panel.hide()
      result(nil)
    case "insert":
      result(insert(args["text"] as? String ?? ""))
    default:
      result(FlutterMethodNotImplemented)
    }
  }

  /// Where the text is going (#362), read when the shortcut is pressed:
  /// the app, its window's title, the focused field's kind and label, and
  /// the text around the insertion point — up to 600 characters before and
  /// 200 after. A secure text field (a password) yields nothing but the
  /// flag that it is one.
  private func focus() -> [String: Any] {
    var out: [String: Any] = [:]
    guard let app = NSWorkspace.shared.frontmostApplication else { return out }
    out["app"] = app.localizedName ?? ""
    if Self.excluded.contains(app.bundleIdentifier ?? "") {
      out["secure"] = true
      return out
    }
    guard AXIsProcessTrusted() else { return out }

    func attr(_ el: AXUIElement, _ name: String) -> AnyObject? {
      var v: AnyObject?
      return AXUIElementCopyAttributeValue(el, name as CFString, &v) == .success ? v : nil
    }
    let appElement = AXUIElementCreateApplication(app.processIdentifier)
    AXUIElementSetMessagingTimeout(appElement, 0.25)
    if let window = attr(appElement, kAXFocusedWindowAttribute) {
      out["window"] = attr(window as! AXUIElement, kAXTitleAttribute) as? String ?? ""
    }
    guard let focused = attr(appElement, kAXFocusedUIElementAttribute) else { return out }
    let el = focused as! AXUIElement
    let role = attr(el, kAXRoleAttribute) as? String ?? ""
    let subrole = attr(el, kAXSubroleAttribute) as? String ?? ""
    if subrole == kAXSecureTextFieldSubrole {
      out["secure"] = true
      return out
    }
    let label = [kAXTitleAttribute, kAXDescriptionAttribute, "AXPlaceholderValue"]
      .compactMap { attr(el, $0) as? String }
      .first { !$0.isEmpty } ?? ""
    let kind = attr(el, kAXRoleDescriptionAttribute) as? String ?? role
    out["field"] = [kind, label].filter { !$0.isEmpty }.joined(separator: " · ")

    if let value = attr(el, kAXValueAttribute) as? String,
       let rangeValue = attr(el, kAXSelectedTextRangeAttribute) {
      var range = CFRange()
      if AXValueGetValue(rangeValue as! AXValue, .cfRange, &range) {
        let ns = value as NSString
        let start = min(max(range.location, 0), ns.length)
        let end = min(start + max(range.length, 0), ns.length)
        let from = max(0, start - 600)
        out["before"] = ns.substring(with: NSRange(location: from, length: start - from))
        out["after"] = ns.substring(with: NSRange(location: end, length: min(200, ns.length - end)))
      }
    }
    return out
  }

  /// Puts [text] where the cursor is: onto the pasteboard, then ⌘V into the
  /// frontmost app. What the pasteboard held before is put back once the
  /// paste has been read — unless something else has written to it since.
  private func insert(_ text: String) -> Bool {
    guard AXIsProcessTrusted(), !text.isEmpty else { return false }
    let pb = NSPasteboard.general
    let saved: [NSPasteboardItem] = (pb.pasteboardItems ?? []).map { item in
      let copy = NSPasteboardItem()
      for type in item.types {
        if let data = item.data(forType: type) { copy.setData(data, forType: type) }
      }
      return copy
    }
    pb.clearContents()
    pb.setString(text, forType: .string)
    let ours = pb.changeCount

    let source = CGEventSource(stateID: .combinedSessionState)
    let v: CGKeyCode = 9  // the V key; the same position on QWERTY and QWERTZ
    let down = CGEvent(keyboardEventSource: source, virtualKey: v, keyDown: true)
    let up = CGEvent(keyboardEventSource: source, virtualKey: v, keyDown: false)
    down?.flags = .maskCommand
    up?.flags = .maskCommand
    down?.post(tap: .cghidEventTap)
    up?.post(tap: .cghidEventTap)

    DispatchQueue.main.asyncAfter(deadline: .now() + 0.8) {
      guard pb.changeCount == ours, !saved.isEmpty else { return }
      pb.clearContents()
      pb.writeObjects(saved)
    }
    return true
  }
}

/// A small capsule at the bottom of the screen (#355, #358): the voice as a
/// living waveform, and the words once there are some. It floats above every
/// app and every Space, takes no clicks and never becomes key — the text is
/// for the app that has the focus. It starts as the waveform alone and
/// widens when words arrive; it rises in and sinks out.
final class FlowPanel {
  private let panel: NSPanel
  private let glass = NSVisualEffectView()
  /// Liquid Glass where the system has it (macOS 26 and later); typed as
  /// NSView so the app still runs on older systems.
  private var liquid: NSView?
  private let content = NSView()
  private let wave = WaveView()
  private let label = NSTextField(wrappingLabelWithString: "")
  private let icon = NSImageView()

  private static let height: CGFloat = 44
  private static let compactWidth: CGFloat = 166
  private static let maxWidth: CGFloat = 560
  private static let waveWidth: CGFloat = 92
  /// The target app's icon, left of the wave: where the text goes.
  private static let iconSide: CGFloat = 24
  private static let iconGap: CGFloat = 10
  private static var lead: CGFloat { iconSide + iconGap }
  private static let pad: CGFloat = 20
  private static let gap: CGFloat = 12
  private static let maxLines = 5
  private static let font = NSFont.systemFont(ofSize: 14, weight: .medium)
  private static var lineHeight: CGFloat { ceil(font.ascender - font.descender + font.leading) + 3 }
  private var text = ""
  private var bottom: CGFloat = 0

  init() {
    panel = NSPanel(
      contentRect: NSRect(x: 0, y: 0, width: Self.compactWidth, height: Self.height),
      styleMask: [.nonactivatingPanel, .borderless],
      backing: .buffered, defer: true)
    panel.isFloatingPanel = true
    panel.level = .statusBar
    panel.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary, .stationary]
    panel.ignoresMouseEvents = true
    panel.hasShadow = true
    panel.backgroundColor = .clear
    panel.isOpaque = false
    panel.hidesOnDeactivate = false

    // Dark in light and dark mode alike, as the system's own HUDs are: the
    // capsule is an instrument, not a window.
    glass.material = .hudWindow
    glass.appearance = NSAppearance(named: .vibrantDark)
    glass.state = .active
    glass.blendingMode = .behindWindow
    // Blending behind the window ignores the layer's corner radius — the
    // material fills the whole rectangle, and its corners show as pale
    // patches. A mask image is what clips it (#360); the border and the
    // shadow follow the same shape.
    glass.wantsLayer = true
    glass.maskImage = Self.mask(radius: Self.height / 2)
    glass.autoresizingMask = [.width, .height]
    glass.frame = panel.contentLayoutRect

    label.font = Self.font
    label.textColor = NSColor.white.withAlphaComponent(0.92)
    label.maximumNumberOfLines = 0
    label.alphaValue = 0

    if #available(macOS 26.0, *) {
      // Liquid Glass (#361): the system's own material, refracting what is
      // behind it. Tinted dark, so white bars and text read on any
      // background — the capsule stays an instrument over bright pages too.
      let g = NSGlassEffectView()
      g.cornerRadius = Self.height / 2
      g.tintColor = NSColor.black.withAlphaComponent(0.4)
      g.appearance = NSAppearance(named: .darkAqua)
      g.frame = panel.contentLayoutRect
      g.autoresizingMask = [.width, .height]
      content.frame = g.bounds
      content.autoresizingMask = [.width, .height]
      content.addSubview(icon)
      content.addSubview(wave)
      content.addSubview(label)
      g.contentView = content
      panel.contentView = g
      liquid = g
    } else {
      glass.addSubview(icon)
      glass.addSubview(wave)
      glass.addSubview(label)
      panel.contentView = glass
    }
  }

  /// What the text needs: one line as wide as it is, or up to five lines at
  /// full width — and beyond that the end of it, "…" in front: the end is
  /// what is being said now.
  private func measure(_ full: String) -> (shown: String, size: NSSize) {
    let maxLabel = Self.maxWidth - Self.pad - Self.lead - Self.waveWidth - Self.gap - Self.pad
    let attrs: [NSAttributedString.Key: Any] = [.font: Self.font]
    func height(_ s: String) -> CGFloat {
      ceil(NSAttributedString(string: s, attributes: attrs).boundingRect(
        with: NSSize(width: maxLabel, height: .greatestFiniteMagnitude),
        options: [.usesLineFragmentOrigin, .usesFontLeading]).height)
    }
    let oneLine = ceil(NSAttributedString(string: full, attributes: attrs).size().width) + 4
    if oneLine <= maxLabel { return (full, NSSize(width: oneLine, height: Self.lineHeight)) }
    let limit = Self.lineHeight * CGFloat(Self.maxLines)
    if height(full) <= limit { return (full, NSSize(width: maxLabel, height: height(full))) }
    // The longest tail that fits, found by halving; cut at a word.
    let chars = Array(full)
    var lo = 0, hi = chars.count
    while lo < hi {
      let mid = (lo + hi) / 2
      if height("…" + String(chars[mid...])) <= limit { hi = mid } else { lo = mid + 1 }
    }
    var tail = String(chars[lo...])
    if let space = tail.firstIndex(of: " "), tail.distance(from: tail.startIndex, to: space) < 20 {
      tail = String(tail[tail.index(after: space)...])
    }
    let shown = "…" + tail
    return (shown, NSSize(width: maxLabel, height: height(shown)))
  }

  private func frame(for size: NSSize?) -> NSRect {
    let width: CGFloat, height: CGFloat
    if let size {
      width = Self.pad + Self.lead + Self.waveWidth + Self.gap + size.width + Self.pad
      height = max(Self.height, size.height + 24)
    } else {
      width = Self.compactWidth
      height = Self.height
    }
    let mouse = NSEvent.mouseLocation
    let screen = NSScreen.screens.first { NSMouseInRect(mouse, $0.frame, false) } ?? NSScreen.main
    let f = screen?.visibleFrame ?? .zero
    return NSRect(x: f.midX - width / 2, y: bottom, width: width, height: height)
  }

  private func layout(_ rect: NSRect, labelSize: NSSize?) {
    let waveH = Self.height - 20
    // Icon and wave stay at the bottom, beside the last line; the text grows
    // upwards.
    let groupX = labelSize == nil ? (rect.width - Self.lead - Self.waveWidth) / 2 : Self.pad
    icon.frame = NSRect(
      x: groupX, y: (Self.height - Self.iconSide) / 2, width: Self.iconSide, height: Self.iconSide)
    wave.frame = NSRect(x: groupX + Self.lead, y: 10, width: Self.waveWidth, height: waveH)
    if let size = labelSize {
      label.frame = NSRect(
        x: Self.pad + Self.lead + Self.waveWidth + Self.gap, y: (rect.height - size.height) / 2,
        width: size.width, height: size.height)
    }
    let radius = min(Self.height / 2, rect.height / 2)
    if radius != maskRadius {
      maskRadius = radius
      if #available(macOS 26.0, *), let g = liquid as? NSGlassEffectView {
        g.cornerRadius = radius
      } else {
        glass.maskImage = Self.mask(radius: radius)
      }
    }
    panel.invalidateShadow()
  }

  private var maskRadius: CGFloat = 0

  /// A rounded rectangle that stretches in the middle and keeps its corners:
  /// the shape of the capsule at any size, with a hairline edge.
  private static func mask(radius: CGFloat) -> NSImage {
    let side = radius * 2 + 1
    let image = NSImage(size: NSSize(width: side, height: side), flipped: false) { rect in
      NSColor.black.setFill()
      NSBezierPath(roundedRect: rect, xRadius: radius, yRadius: radius).fill()
      return true
    }
    image.capInsets = NSEdgeInsets(top: radius, left: radius, bottom: radius, right: radius)
    image.resizingMode = .stretch
    return image
  }

  func show() {
    icon.image = NSWorkspace.shared.frontmostApplication?.icon
    text = ""
    label.stringValue = ""
    label.alphaValue = 0
    let mouse = NSEvent.mouseLocation
    let screen = NSScreen.screens.first { NSMouseInRect(mouse, $0.frame, false) } ?? NSScreen.main
    bottom = (screen?.visibleFrame.minY ?? 0) + 52
    var rect = frame(for: nil)
    panel.setFrame(rect, display: false)
    layout(rect, labelSize: nil)
    panel.alphaValue = 0
    panel.orderFrontRegardless()
    wave.start()
    bottom += 12
    rect = frame(for: nil)
    NSAnimationContext.runAnimationGroup { ctx in
      ctx.duration = 0.28
      ctx.timingFunction = CAMediaTimingFunction(controlPoints: 0.2, 0.9, 0.3, 1.0)
      panel.animator().alphaValue = 1
      panel.animator().setFrame(rect, display: true)
    }
  }

  func update(text newText: String, level: Double, busy: Bool) {
    wave.busy = busy
    wave.push(level)
    guard newText != text else { return }
    let wasEmpty = text.isEmpty
    text = newText
    let measured: (shown: String, size: NSSize)? = newText.isEmpty ? nil : measure(newText)
    label.stringValue = measured?.shown ?? ""
    let rect = frame(for: measured?.size)
    let old = panel.frame
    // Growing is animated; the shrinking of a line that whisper re-reads
    // shorter is not worth a jitter.
    if abs(rect.width - old.width) > 6 || abs(rect.height - old.height) > 2 {
      if rect.width >= old.width || rect.height != old.height || newText.isEmpty {
        NSAnimationContext.runAnimationGroup { ctx in
          ctx.duration = 0.2
          ctx.timingFunction = CAMediaTimingFunction(name: .easeOut)
          panel.animator().setFrame(rect, display: true)
        }
        layout(rect, labelSize: measured?.size)
      } else {
        layout(old, labelSize: measured.map { NSSize(width: old.width - (rect.width - $0.size.width), height: $0.size.height) })
      }
    } else {
      layout(old, labelSize: measured?.size)
    }
    if wasEmpty != newText.isEmpty {
      NSAnimationContext.runAnimationGroup { ctx in
        ctx.duration = 0.18
        label.animator().alphaValue = newText.isEmpty ? 0 : 1
      }
    }
  }

  func hide() {
    bottom -= 12
    let rect = NSRect(x: panel.frame.minX, y: bottom, width: panel.frame.width, height: panel.frame.height)
    NSAnimationContext.runAnimationGroup({ ctx in
      ctx.duration = 0.2
      ctx.timingFunction = CAMediaTimingFunction(name: .easeIn)
      panel.animator().alphaValue = 0
      panel.animator().setFrame(rect, display: true)
    }, completionHandler: { [panel, wave] in
      panel.orderOut(nil)
      wave.stop()
    })
  }
}

/// The voice as bars that breathe with it: drawn sixty times a second, each
/// bar easing towards the level — quick up, slow down — shaped higher in the
/// middle, and each with a sway of its own so the whole looks alive rather
/// than stepped. Busy (the text is being cleaned up), a calm wave runs
/// through instead.
final class WaveView: NSView {
  var busy = false
  private var target: Double = 0
  private var level: Double = 0
  private var bars: [CALayer] = []
  private var heights: [Double] = []
  private var timer: Timer?
  private var t: Double = 0
  private let count = 13
  private let phases: [Double] = (0..<13).map { _ in Double.random(in: 0..<(2 * .pi)) }
  private let speeds: [Double] = (0..<13).map { _ in Double.random(in: 5.5...9.5) }

  override init(frame: NSRect) {
    super.init(frame: frame)
    wantsLayer = true
    for _ in 0..<count {
      let bar = CALayer()
      bar.backgroundColor = NSColor.white.cgColor
      bar.cornerCurve = .continuous
      layer?.addSublayer(bar)
      bars.append(bar)
      heights.append(0)
    }
  }

  required init?(coder: NSCoder) { fatalError("init(coder:) has not been implemented") }

  func push(_ value: Double) { target = min(max(value, 0), 1) }

  func start() {
    level = 0
    target = 0
    t = 0
    timer?.invalidate()
    let timer = Timer(timeInterval: 1.0 / 60, repeats: true) { [weak self] _ in self?.tick() }
    RunLoop.main.add(timer, forMode: .common)
    self.timer = timer
  }

  func stop() {
    timer?.invalidate()
    timer = nil
  }

  private func tick() {
    t += 1.0 / 60
    // Attack fast, release slow: speech lifts the bars at once, and a pause
    // lets them settle instead of dropping.
    let k = target > level ? 0.45 : 0.08
    level += (target - level) * k
    let w = bounds.width, h = bounds.height
    let gap: CGFloat = 3.5
    let barW = (w - gap * CGFloat(count - 1)) / CGFloat(count)
    CATransaction.begin()
    CATransaction.setDisableActions(true)
    for i in 0..<count {
      let x = Double(i) / Double(count - 1)  // 0…1
      let bell = exp(-pow((x - 0.5) / 0.28, 2))  // higher in the middle
      var v: Double
      if busy {
        v = 0.18 + 0.22 * (0.5 + 0.5 * sin(t * 5 - Double(i) * 0.55))
      } else {
        let sway = 0.6 + 0.4 * sin(t * speeds[i] + phases[i])
        v = 0.12 + 0.88 * level * bell * sway
      }
      heights[i] += (v - heights[i]) * 0.35
      let bh = max(barW, CGFloat(heights[i]) * h)
      let bar = bars[i]
      bar.frame = CGRect(x: CGFloat(i) * (barW + gap), y: (h - bh) / 2, width: barW, height: bh)
      bar.cornerRadius = barW / 2
      bar.opacity = Float(0.55 + 0.45 * min(1, heights[i] * 2.2))
    }
    CATransaction.commit()
  }
}

/// The menu-bar item of the activity log (#363): visible while it records,
/// so recording is never silent, with pause and off one click away. The
/// strings come from Dart, in the app's language.
final class ActivityStatusItem {
  private var item: NSStatusItem?
  private let onAction: (String) -> Void
  private var targets: [ActionTarget] = []

  init(onAction: @escaping (String) -> Void) { self.onAction = onAction }

  /// covey's signet for the menu bar: the three birds of the app icon
  /// (lib/mark.dart), without the tile, as a template image — macOS colours
  /// it for a light or dark menu bar.
  static let mark: NSImage = {
    let birds: [[CGFloat]] = [
      [7.0, 15.0, 9.75, 11.8, 12.5, 15.0, 15.25, 11.8, 18.0, 15.0],
      [3.5, 10.0, 5.5, 7.7, 7.5, 10.0, 9.5, 7.7, 11.5, 10.0],
      [13.0, 8.0, 14.5, 6.3, 16.0, 8.0, 17.5, 6.3, 19.0, 8.0],
    ]
    let image = NSImage(size: NSSize(width: 20, height: 18), flipped: true) { _ in
      let path = NSBezierPath()
      // The birds' box is x 3.5…19, y 6.3…15: centred in the image.
      let dx: CGFloat = -1.25, dy: CGFloat = -1.65
      func p(_ x: CGFloat, _ y: CGFloat) -> NSPoint { NSPoint(x: x + dx, y: y + dy) }
      // A quadratic curve as the cubic NSBezierPath draws.
      func quad(from a: NSPoint, control q: NSPoint, to b: NSPoint) {
        path.curve(
          to: b,
          controlPoint1: NSPoint(x: a.x + 2 / 3 * (q.x - a.x), y: a.y + 2 / 3 * (q.y - a.y)),
          controlPoint2: NSPoint(x: b.x + 2 / 3 * (q.x - b.x), y: b.y + 2 / 3 * (q.y - b.y)))
      }
      for b in birds {
        let start = p(b[0], b[1]), mid = p(b[4], b[5]), end = p(b[8], b[9])
        path.move(to: start)
        quad(from: start, control: p(b[2], b[3]), to: mid)
        quad(from: mid, control: p(b[6], b[7]), to: end)
      }
      path.lineWidth = 1.9
      path.lineCapStyle = .round
      path.lineJoinStyle = .round
      NSColor.black.setStroke()
      path.stroke()
      return true
    }
    image.isTemplate = true
    return image
  }()

  func update(_ args: [String: Any]) {
    guard args["visible"] as? Bool == true else {
      if let item { NSStatusBar.system.removeStatusItem(item) }
      item = nil
      return
    }
    if item == nil {
      item = NSStatusBar.system.statusItem(withLength: NSStatusItem.squareLength)
    }
    let paused = args["paused"] as? Bool == true
    item?.button?.image = Self.mark
    item?.button?.image?.accessibilityDescription = args["title"] as? String
    // Paused, the birds go pale — still there, visibly not recording.
    item?.button?.appearsDisabled = paused

    let menu = NSMenu()
    let head = NSMenuItem(title: args["title"] as? String ?? "", action: nil, keyEquivalent: "")
    head.isEnabled = false
    menu.addItem(head)
    menu.addItem(.separator())
    targets = []
    for (key, id) in [("pause", "pause"), ("resume", "resume"), ("review", "review"), ("off", "off")] {
      guard let title = args[key] as? String, !title.isEmpty else { continue }
      let target = ActionTarget { [weak self] in self?.onAction(id) }
      targets.append(target)
      let mi = NSMenuItem(title: title, action: #selector(ActionTarget.fire), keyEquivalent: "")
      mi.target = target
      menu.addItem(mi)
    }
    item?.menu = menu
  }
}

final class ActionTarget: NSObject {
  private let run: () -> Void
  init(_ run: @escaping () -> Void) { self.run = run }
  @objc func fire() { run() }
}

/// The Mac's own audio for a meeting (#364): what the Mac plays — the other
/// side of a call in Teams, Zoom or Meet — taken with a Core Audio process
/// tap, converted to 16 kHz mono PCM16 and handed to Dart as it comes.
/// Listening starts the tap, cancelling stops it; macOS asks once for the
/// permission to record other apps' audio.
final class SystemAudioHandler: NSObject, FlutterStreamHandler {
  private var tap: AnyObject?

  func onListen(withArguments arguments: Any?, eventSink events: @escaping FlutterEventSink) -> FlutterError? {
    guard #available(macOS 14.4, *) else {
      return FlutterError(code: "unsupported", message: "the Mac's audio needs macOS 14.4 or later", details: nil)
    }
    let t = SystemAudioTap()
    do {
      try t.start { data in
        DispatchQueue.main.async { events(FlutterStandardTypedData(bytes: data)) }
      }
    } catch {
      t.stop()
      return FlutterError(code: "tap", message: "\(error)", details: nil)
    }
    tap = t
    return nil
  }

  func onCancel(withArguments arguments: Any?) -> FlutterError? {
    if #available(macOS 14.4, *) { (tap as? SystemAudioTap)?.stop() }
    tap = nil
    return nil
  }
}

@available(macOS 14.4, *)
final class SystemAudioTap {
  private var tapID = AudioObjectID(kAudioObjectUnknown)
  private var aggregateID = AudioObjectID(kAudioObjectUnknown)
  private var procID: AudioDeviceIOProcID?
  private let queue = DispatchQueue(label: "work.covey.system-audio")

  struct Failure: Error, CustomStringConvertible {
    let step: String
    let status: OSStatus
    var description: String { "\(step) failed (\(status))" }
  }

  private static func check(_ status: OSStatus, _ step: String) throws {
    if status != noErr { throw Failure(step: step, status: status) }
  }

  private static func property<T>(_ object: AudioObjectID, _ selector: AudioObjectPropertySelector, _ value: inout T) throws {
    var address = AudioObjectPropertyAddress(
      mSelector: selector, mScope: kAudioObjectPropertyScopeGlobal, mElement: kAudioObjectPropertyElementMain)
    var size = UInt32(MemoryLayout<T>.size)
    try check(AudioObjectGetPropertyData(object, &address, 0, nil, &size, &value), "reading property \(selector)")
  }

  func start(onChunk: @escaping (Data) -> Void) throws {
    // Everything the Mac plays, mixed to stereo; covey itself plays nothing.
    let description = CATapDescription(stereoGlobalTapButExcludeProcesses: [])
    description.uuid = UUID()
    description.muteBehavior = .unmuted
    description.isPrivate = true
    description.name = "covey meeting"
    try Self.check(AudioHardwareCreateProcessTap(description, &tapID), "creating the tap")

    // The tap is read through a private aggregate device on the output.
    var output = AudioObjectID(kAudioObjectUnknown)
    try Self.property(AudioObjectID(kAudioObjectSystemObject), kAudioHardwarePropertyDefaultSystemOutputDevice, &output)
    var outputUID: CFString = "" as CFString
    try Self.property(output, kAudioDevicePropertyDeviceUID, &outputUID)
    let aggregate: [String: Any] = [
      kAudioAggregateDeviceNameKey: "covey meeting",
      kAudioAggregateDeviceUIDKey: UUID().uuidString,
      kAudioAggregateDeviceMainSubDeviceKey: outputUID as String,
      kAudioAggregateDeviceIsPrivateKey: true,
      kAudioAggregateDeviceIsStackedKey: false,
      kAudioAggregateDeviceTapAutoStartKey: true,
      kAudioAggregateDeviceSubDeviceListKey: [[kAudioSubDeviceUIDKey: outputUID as String]],
      kAudioAggregateDeviceTapListKey: [[
        kAudioSubTapDriftCompensationKey: true,
        kAudioSubTapUIDKey: description.uuid.uuidString,
      ]],
    ]
    try Self.check(AudioHardwareCreateAggregateDevice(aggregate as CFDictionary, &aggregateID), "creating the device")

    var format = AudioStreamBasicDescription()
    try Self.property(tapID, kAudioTapPropertyFormat, &format)
    guard let inFormat = AVAudioFormat(streamDescription: &format),
      let outFormat = AVAudioFormat(commonFormat: .pcmFormatInt16, sampleRate: 16000, channels: 1, interleaved: true),
      let converter = AVAudioConverter(from: inFormat, to: outFormat)
    else { throw Failure(step: "setting up the conversion", status: -1) }

    try Self.check(
      AudioDeviceCreateIOProcIDWithBlock(&procID, aggregateID, queue) { _, input, _, _, _ in
        guard let buffer = AVAudioPCMBuffer(pcmFormat: inFormat, bufferListNoCopy: input, deallocator: nil),
          buffer.frameLength > 0,
          let out = AVAudioPCMBuffer(
            pcmFormat: outFormat,
            frameCapacity: AVAudioFrameCount(Double(buffer.frameLength) * 16000 / inFormat.sampleRate) + 32)
        else { return }
        var fed = false
        converter.convert(to: out, error: nil) { _, status in
          if fed {
            status.pointee = .noDataNow
            return nil
          }
          fed = true
          status.pointee = .haveData
          return buffer
        }
        if out.frameLength > 0, let samples = out.int16ChannelData?[0] {
          onChunk(Data(bytes: samples, count: Int(out.frameLength) * 2))
        }
      }, "reading the device")
    try Self.check(AudioDeviceStart(aggregateID, procID), "starting the device")
  }

  func stop() {
    if aggregateID != kAudioObjectUnknown {
      AudioDeviceStop(aggregateID, procID)
      if let procID { AudioDeviceDestroyIOProcID(aggregateID, procID) }
      AudioHardwareDestroyAggregateDevice(aggregateID)
      aggregateID = AudioObjectID(kAudioObjectUnknown)
    }
    procID = nil
    if tapID != kAudioObjectUnknown {
      AudioHardwareDestroyProcessTap(tapID)
      tapID = AudioObjectID(kAudioObjectUnknown)
    }
  }
}

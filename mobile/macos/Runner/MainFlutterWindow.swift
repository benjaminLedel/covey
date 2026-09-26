import ApplicationServices
import Cocoa
import FlutterMacOS

class MainFlutterWindow: NSWindow {
  private var flow: FlowBridge?
  private var chrome: FlutterMethodChannel?

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

    super.awakeFromNib()
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
    item?.button?.image = NSImage(
      systemSymbolName: paused ? "pause.circle" : "record.circle", accessibilityDescription: args["title"] as? String)
    item?.button?.image?.isTemplate = true

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

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

  init(messenger: FlutterBinaryMessenger) {
    channel = FlutterMethodChannel(name: "covey/flow", binaryMessenger: messenger)
    channel.setMethodCallHandler { [weak self] call, result in
      self?.handle(call, result)
    }
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
  private let wave = WaveView()
  private let label = NSTextField(labelWithString: "")

  private static let height: CGFloat = 44
  private static let compactWidth: CGFloat = 132
  private static let maxWidth: CGFloat = 520
  private var text = ""

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
    glass.wantsLayer = true
    glass.layer?.cornerRadius = Self.height / 2
    glass.layer?.cornerCurve = .continuous
    glass.layer?.masksToBounds = true
    glass.layer?.borderWidth = 0.5
    glass.layer?.borderColor = NSColor.white.withAlphaComponent(0.12).cgColor
    glass.autoresizingMask = [.width, .height]
    glass.frame = panel.contentLayoutRect

    label.font = .systemFont(ofSize: 14, weight: .medium)
    label.textColor = NSColor.white.withAlphaComponent(0.92)
    label.lineBreakMode = .byTruncatingHead  // the end is what is being said now
    label.maximumNumberOfLines = 1
    label.cell?.truncatesLastVisibleLine = true
    label.alphaValue = 0

    glass.addSubview(wave)
    glass.addSubview(label)
    panel.contentView = glass
    layout(width: Self.compactWidth)
  }

  private func layout(width: CGFloat) {
    let waveWidth: CGFloat = 92
    let padding: CGFloat = 20
    if text.isEmpty {
      wave.frame = NSRect(x: (width - waveWidth) / 2, y: 10, width: waveWidth, height: Self.height - 20)
    } else {
      wave.frame = NSRect(x: padding, y: 10, width: waveWidth, height: Self.height - 20)
    }
    let labelX = padding + waveWidth + 12
    label.frame = NSRect(x: labelX, y: (Self.height - 20) / 2, width: max(0, width - labelX - padding), height: 20)
  }

  private func origin(width: CGFloat, lifted: Bool) -> NSPoint {
    let mouse = NSEvent.mouseLocation
    let screen = NSScreen.screens.first { NSMouseInRect(mouse, $0.frame, false) } ?? NSScreen.main
    let f = screen?.visibleFrame ?? .zero
    return NSPoint(x: f.midX - width / 2, y: f.minY + (lifted ? 64 : 52))
  }

  func show() {
    text = ""
    label.stringValue = ""
    label.alphaValue = 0
    let width = Self.compactWidth
    panel.setFrame(NSRect(origin: origin(width: width, lifted: false), size: NSSize(width: width, height: Self.height)), display: false)
    layout(width: width)
    panel.alphaValue = 0
    panel.orderFrontRegardless()
    wave.start()
    NSAnimationContext.runAnimationGroup { ctx in
      ctx.duration = 0.28
      ctx.timingFunction = CAMediaTimingFunction(controlPoints: 0.2, 0.9, 0.3, 1.0)
      panel.animator().alphaValue = 1
      panel.animator().setFrameOrigin(origin(width: width, lifted: true))
    }
  }

  func update(text newText: String, level: Double, busy: Bool) {
    wave.busy = busy
    wave.push(level)
    guard newText != text else { return }
    let wasEmpty = text.isEmpty
    text = newText
    label.stringValue = newText
    // Wider for the words, up to a line's worth; measured, not guessed.
    let wanted = newText.isEmpty
      ? Self.compactWidth
      : min(Self.maxWidth, 20 + 92 + 12 + ceil(label.intrinsicContentSize.width) + 20)
    let current = panel.frame.width
    if abs(wanted - current) > 6 && (wanted > current || newText.isEmpty) {
      NSAnimationContext.runAnimationGroup { ctx in
        ctx.duration = 0.22
        ctx.timingFunction = CAMediaTimingFunction(name: .easeOut)
        panel.animator().setFrame(
          NSRect(origin: origin(width: wanted, lifted: true), size: NSSize(width: wanted, height: Self.height)),
          display: true)
      }
      layout(width: wanted)
    } else {
      layout(width: current)
    }
    if wasEmpty != newText.isEmpty {
      NSAnimationContext.runAnimationGroup { ctx in
        ctx.duration = 0.18
        label.animator().alphaValue = newText.isEmpty ? 0 : 1
      }
    }
  }

  func hide() {
    let width = panel.frame.width
    NSAnimationContext.runAnimationGroup({ ctx in
      ctx.duration = 0.2
      ctx.timingFunction = CAMediaTimingFunction(name: .easeIn)
      panel.animator().alphaValue = 0
      panel.animator().setFrameOrigin(origin(width: width, lifted: false))
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

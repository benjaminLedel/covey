import ApplicationServices
import Cocoa
import FlutterMacOS

class MainFlutterWindow: NSWindow {
  private var flow: FlowBridge?

  override func awakeFromNib() {
    let flutterViewController = FlutterViewController()
    let windowFrame = self.frame
    self.contentViewController = flutterViewController
    self.setFrame(windowFrame, display: true)
    // Closing the window hides it: the app keeps answering the dictation
    // shortcut (#355), and the Dock icon brings it back.
    self.isReleasedWhenClosed = false

    RegisterGeneratedPlugins(registry: flutterViewController)
    flow = FlowBridge(messenger: flutterViewController.engine.binaryMessenger)

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
        levels: (args["levels"] as? [Double]) ?? [],
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

/// A small pill at the bottom of the screen: the waveform and the last words
/// heard. It floats above every app and every Space, takes no clicks and
/// never becomes key — the text is for the app that has the focus.
final class FlowPanel {
  private let panel: NSPanel
  private let wave = WaveView()
  private let label = NSTextField(labelWithString: "")
  private let spinner = NSProgressIndicator()

  init() {
    panel = NSPanel(
      contentRect: NSRect(x: 0, y: 0, width: 440, height: 52),
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

    let glass = NSVisualEffectView(frame: panel.contentLayoutRect)
    glass.material = .hudWindow
    glass.state = .active
    glass.blendingMode = .behindWindow
    glass.wantsLayer = true
    glass.layer?.cornerRadius = 26
    glass.layer?.masksToBounds = true
    glass.autoresizingMask = [.width, .height]

    wave.frame = NSRect(x: 18, y: 14, width: 96, height: 24)
    spinner.style = .spinning
    spinner.controlSize = .small
    spinner.frame = NSRect(x: 18, y: 17, width: 18, height: 18)
    spinner.isHidden = true

    label.frame = NSRect(x: 126, y: 15, width: 296, height: 22)
    label.font = .systemFont(ofSize: 15, weight: .medium)
    label.textColor = .labelColor
    label.lineBreakMode = .byTruncatingHead  // the end is what is being said now
    label.maximumNumberOfLines = 1
    label.cell?.truncatesLastVisibleLine = true

    glass.addSubview(wave)
    glass.addSubview(spinner)
    glass.addSubview(label)
    panel.contentView = glass
  }

  func show() {
    let mouse = NSEvent.mouseLocation
    let screen = NSScreen.screens.first { NSMouseInRect(mouse, $0.frame, false) } ?? NSScreen.main
    if let f = screen?.visibleFrame {
      let size = panel.frame.size
      panel.setFrameOrigin(NSPoint(x: f.midX - size.width / 2, y: f.minY + 72))
    }
    panel.alphaValue = 1
    panel.orderFrontRegardless()
  }

  func update(text: String, levels: [Double], busy: Bool) {
    label.stringValue = text
    wave.levels = levels
    wave.isHidden = busy
    spinner.isHidden = !busy
    if busy { spinner.startAnimation(nil) } else { spinner.stopAnimation(nil) }
    wave.needsDisplay = true
  }

  func hide() {
    NSAnimationContext.runAnimationGroup({ ctx in
      ctx.duration = 0.18
      panel.animator().alphaValue = 0
    }, completionHandler: { [panel] in
      panel.orderOut(nil)
      panel.alphaValue = 1
    })
  }
}

/// The level of the last second or two, newest on the right.
final class WaveView: NSView {
  var levels: [Double] = []

  override func draw(_ dirtyRect: NSRect) {
    let bar: CGFloat = 3, gap: CGFloat = 2
    let slots = Int(bounds.width / (bar + gap))
    let tail = Array(levels.suffix(slots))
    let pad = slots - tail.count
    NSColor.controlAccentColor.setFill()
    for i in 0..<slots {
      let v = i < pad ? 0 : CGFloat(tail[i - pad])
      let h = max(bar, bounds.height * (0.1 + 0.9 * min(max(v, 0), 1)))
      let x = CGFloat(i) * (bar + gap)
      let rect = NSRect(x: x, y: (bounds.height - h) / 2, width: bar, height: h)
      let alpha = 0.35 + 0.65 * CGFloat(i) / CGFloat(max(slots, 1))
      NSColor.controlAccentColor.withAlphaComponent(v < 0.04 ? 0.25 : alpha).setFill()
      NSBezierPath(roundedRect: rect, xRadius: bar / 2, yRadius: bar / 2).fill()
    }
  }
}

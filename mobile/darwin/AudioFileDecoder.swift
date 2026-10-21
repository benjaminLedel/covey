import AVFoundation
#if os(macOS)
import FlutterMacOS
#else
import Flutter
#endif

/// Audio files decoded on the device for the recogniser (#634): AVFoundation
/// reads the file where it lies and converts it to 16 kHz mono PCM16, block
/// by block as Dart asks for it, so a recording of hours is never in memory
/// whole. What Core Audio cannot read (another format, a damaged file) is
/// refused with the code `unsupported`.
///
/// One file for the iPhone and the Mac (Runner.xcodeproj of each refers to
/// it): Dart's side is `mobile/lib/audio_file.dart`.
///
///   open  {path}         → {id, durationMs}
///   read  {id, frames}   → PCM16 bytes, or nil at the end
///   close {id}
final class AudioFileDecoder {
  private let channel: FlutterMethodChannel
  /// Decoding runs here, off the main thread; the readers are only touched
  /// on it.
  private let queue = DispatchQueue(label: "work.covey.audio-file")
  private var readers: [Int: AudioFileReader] = [:]
  private var next = 1

  init(messenger: FlutterBinaryMessenger) {
    channel = FlutterMethodChannel(name: "covey/audio-file", binaryMessenger: messenger)
    channel.setMethodCallHandler { [weak self] call, result in
      self?.handle(call, result)
    }
  }

  private func handle(_ call: FlutterMethodCall, _ result: @escaping FlutterResult) {
    let args = call.arguments as? [String: Any] ?? [:]
    let reply: (Any?) -> Void = { value in DispatchQueue.main.async { result(value) } }
    queue.async { [self] in
      switch call.method {
      case "open":
        guard let path = args["path"] as? String else {
          return reply(FlutterError(code: "bad-arguments", message: "no path", details: nil))
        }
        do {
          let reader = try AudioFileReader(url: URL(fileURLWithPath: path))
          let id = next
          next += 1
          readers[id] = reader
          reply(["id": id, "durationMs": reader.durationMs])
        } catch {
          reply(FlutterError(code: "unsupported", message: error.localizedDescription, details: nil))
        }
      case "read":
        guard let id = args["id"] as? Int, let reader = readers[id] else { return reply(nil) }
        // Between 0.1 s and 30 s per block.
        let frames = min(max(args["frames"] as? Int ?? 32000, 1600), 480_000)
        do {
          if let data = try reader.read(frames: AVAudioFrameCount(frames)) {
            reply(FlutterStandardTypedData(bytes: data))
          } else {
            reply(nil)
          }
        } catch {
          reply(FlutterError(code: "failed", message: error.localizedDescription, details: nil))
        }
      case "close":
        if let id = args["id"] as? Int {
          readers.removeValue(forKey: id)?.close()
        }
        reply(nil)
      default:
        reply(FlutterMethodNotImplemented)
      }
    }
  }
}

private struct NoAudio: LocalizedError {
  let errorDescription: String?
}

/// One open file: AVAudioFile reads it in the file's own format, and
/// AVAudioConverter resamples, mixes the channels down and turns the samples
/// into 16-bit integers.
private final class AudioFileReader {
  private let url: URL
  private let scoped: Bool
  private var file: AVAudioFile?
  private let converter: AVAudioConverter
  private let input: AVAudioPCMBuffer
  private let output: AVAudioFormat
  private var inputEnded = false
  private var ended = false
  let durationMs: Int

  init(url: URL) throws {
    // A file picked outside the app's container (the Mac's open panel, a
    // security-scoped URL) is readable while access is held.
    let scoped = url.startAccessingSecurityScopedResource()
    do {
      let file = try AVAudioFile(forReading: url)
      let format = file.processingFormat
      guard format.channelCount > 0, format.sampleRate > 0, file.length > 0 else {
        throw NoAudio(errorDescription: "the file holds no audio")
      }
      guard
        let output = AVAudioFormat(commonFormat: .pcmFormatInt16, sampleRate: 16000, channels: 1, interleaved: true),
        let converter = AVAudioConverter(from: format, to: output),
        let input = AVAudioPCMBuffer(pcmFormat: format, frameCapacity: 8192)
      else {
        throw NoAudio(errorDescription: "no conversion from \(format) to 16 kHz mono")
      }
      // All channels mixed into one, rather than the first one alone.
      converter.downmix = true
      self.url = url
      self.scoped = scoped
      self.file = file
      self.output = output
      self.converter = converter
      self.input = input
      durationMs = Int(Double(file.length) / format.sampleRate * 1000)
    } catch {
      if scoped { url.stopAccessingSecurityScopedResource() }
      throw error
    }
  }

  /// Up to [frames] samples of 16 kHz mono PCM16; nil once the file is done.
  func read(frames: AVAudioFrameCount) throws -> Data? {
    guard !ended, let file else { return nil }
    guard let out = AVAudioPCMBuffer(pcmFormat: output, frameCapacity: frames) else { return nil }
    var readError: Error?
    var conversionError: NSError?
    let status = converter.convert(to: out, error: &conversionError) { [self] _, inputStatus in
      if inputEnded || file.framePosition >= file.length {
        inputEnded = true
        inputStatus.pointee = .endOfStream
        return nil
      }
      do {
        try file.read(into: input, frameCount: input.frameCapacity)
      } catch {
        readError = error
        inputEnded = true
        inputStatus.pointee = .endOfStream
        return nil
      }
      if input.frameLength == 0 {
        inputEnded = true
        inputStatus.pointee = .endOfStream
        return nil
      }
      inputStatus.pointee = .haveData
      return input
    }
    if let readError { throw readError }
    if status == .error { throw conversionError ?? NoAudio(errorDescription: "conversion failed") }
    if status == .endOfStream { ended = true }
    guard out.frameLength > 0, let samples = out.int16ChannelData?[0] else { return ended ? nil : Data() }
    return Data(bytes: samples, count: Int(out.frameLength) * MemoryLayout<Int16>.size)
  }

  func close() {
    file = nil
    ended = true
    if scoped { url.stopAccessingSecurityScopedResource() }
  }

  deinit {
    if file != nil, scoped { url.stopAccessingSecurityScopedResource() }
  }
}

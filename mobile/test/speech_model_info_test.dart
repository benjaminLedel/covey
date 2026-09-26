import 'package:covey_mobile/api.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('a model of several files is read with its engine and files (#353)', () {
    final i = SpeechModelInfo.fromJson({
      'enabled': true,
      'name': 'parakeet',
      'engine': 'parakeet',
      'credit': 'NVIDIA Parakeet TDT 0.6B v3 · CC-BY-4.0',
      'sha256': 'ab' * 32,
      'size': 30,
      'ready': true,
      'default': 'base',
      'files': [
        {'name': 'encoder.int8.onnx', 'sha256': 'cd' * 32, 'size': 20},
        {'name': 'tokens.txt', 'sha256': 'ef' * 32, 'size': 10},
      ],
      'models': [
        {'name': 'base', 'engine': 'whisper', 'size': 5, 'files': []},
        {'name': 'parakeet', 'engine': 'parakeet', 'size': 30, 'files': []},
      ],
    });
    expect(i.engine, 'parakeet');
    expect(i.credit, contains('CC-BY-4.0'));
    expect([for (final f in i.files) f.name], ['encoder.int8.onnx', 'tokens.txt']);
    expect(i.files.first.size, 20);
    expect(i.defaultName, 'base');
    expect([for (final m in i.models) m.engine], ['whisper', 'parakeet']);
  });

  test('an instance from before #353 reads as a whisper model without files', () {
    final i = SpeechModelInfo.fromJson({'enabled': true, 'name': 'base', 'sha256': 'ab' * 32, 'size': 5});
    expect(i.engine, 'whisper');
    expect(i.files, isEmpty);
  });
}

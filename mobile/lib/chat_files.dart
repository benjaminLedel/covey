import 'dart:io';

import 'package:file_picker/file_picker.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:image_picker/image_picker.dart';

import 'api.dart';
import 'diagnostics.dart';
import 'i18n.dart';
import 'icons.dart';
import 'models.dart';
import 'theme.dart';
import 'ui.dart';

// The files a chat message carries (#586), at the message: a picture as a
// picture that opens full screen, anything else as a chip that saves the
// file. Every byte is fetched with the key — the attachment route sits
// behind it like the rest of the API — so nothing here hands a widget a URL.
//
// A video is a chip too: playing it in place would take a player plugin the
// app does not carry; saved, the system's own player opens it.

/// Where the files for a message come from.
enum AttachSource { photos, camera, files }

/// The sentence to show when [files] do not fit the server's limits
/// ([chatMaxFiles], [chatMaxBytes]), or null when they do. A size the
/// platform did not tell counts as nothing here; the server checks again.
String? attachmentLimit(Strings strings, List<Attachment> files) {
  if (files.length > chatMaxFiles) return strings.t('chat.files.tooMany', args: {'max': chatMaxFiles});
  final total = files.fold<int>(0, (n, f) => n + (f.length ?? 0));
  if (total > chatMaxBytes) return strings.t('chat.files.tooLarge', args: {'max': chatMaxBytes ~/ (1024 * 1024)});
  return null;
}

/// A size as the web says it (web/src/team/Attachments.tsx).
String fileSize(int n) => n < 1024
    ? '$n B'
    : n < 1024 * 1024
    ? '${(n / 1024).round()} KB'
    : '${(n / (1024 * 1024)).toStringAsFixed(1)} MB';

/// Offers the places a file can come from; null when the sheet is dismissed.
/// The camera only where the system has a camera screen to borrow — a phone.
Future<AttachSource?> chooseAttachSource(BuildContext context) {
  final t = Strings.of(context).t;
  final camera = Platform.isIOS || Platform.isAndroid;
  return showModalBottomSheet<AttachSource>(
    context: context,
    builder: (context) => SafeArea(
      child: Padding(
        padding: const EdgeInsets.only(bottom: 16),
        child: InsetGroup(
          children: [
            GroupRow(
              leading: Icon(AppIcons.photo.of(context)),
              title: t('mobile.anhangFoto'),
              onTap: () => Navigator.pop(context, AttachSource.photos),
            ),
            if (camera)
              GroupRow(
                leading: Icon(AppIcons.camera.of(context)),
                title: t('mobile.fotoAufnehmen'),
                onTap: () => Navigator.pop(context, AttachSource.camera),
              ),
            GroupRow(
              leading: Icon(AppIcons.file.of(context)),
              title: t('mobile.anhangDatei'),
              onTap: () => Navigator.pop(context, AttachSource.files),
            ),
          ],
        ),
      ),
    ),
  );
}

/// The files picked from [source]; empty when nothing was picked. A camera
/// the person did not allow throws a [PlatformException] with the code
/// `camera_access_denied`.
Future<List<Attachment>> pickAttachments(AttachSource source) async {
  switch (source) {
    case AttachSource.camera:
      // A photo of a whiteboard or a receipt: the back camera, scaled so
      // that a few of them fit the 10 MiB a message may carry.
      final shot = await ImagePicker().pickImage(
        source: ImageSource.camera,
        maxWidth: 2560,
        maxHeight: 2560,
        imageQuality: 85,
      );
      if (shot == null) return const [];
      return [Attachment(name: shot.name, length: await shot.length(), open: shot.openRead)];
    case AttachSource.photos:
    case AttachSource.files:
      final files = await FilePicker.pickFiles(type: source == AttachSource.photos ? FileType.media : FileType.any);
      return [for (final f in files) Attachment(name: f.name, length: f.lengthSync(), open: f.readAsByteStream)];
  }
}

/// Hands bytes to the system's save dialog — the share/export sheet on a
/// phone, a save panel on the desktop — and returns where they went, or null
/// when the person cancelled. Swapped in tests.
typedef FileSaver =
    Future<Uri?> Function({required String fileName, required Uint8List bytes, required String mimeType});

Future<Uri?> _systemSave({required String fileName, required Uint8List bytes, required String mimeType}) =>
    FilePicker.saveFile(fileName: fileName, bytes: bytes, mimeType: mimeType);

FileSaver saveFile = _systemSave;

/// Fetches an attachment (unless its [bytes] are already held) and saves it.
Future<void> saveChatAttachment(BuildContext context, CoveyApi api, ChatAttachment a, {Uint8List? bytes}) async {
  final messenger = ScaffoldMessenger.of(context);
  final t = Strings.of(context).t;
  try {
    final data = bytes ?? await api.chatAttachment(a.id, download: true);
    final where = await saveFile(
      fileName: a.name,
      bytes: data,
      mimeType: a.mime.isEmpty ? 'application/octet-stream' : a.mime,
    );
    if (where != null) messenger.showSnackBar(SnackBar(content: Text(t('mobile.gespeichert'))));
  } on ApiException catch (e) {
    diag('chat', 'attachment not loaded: ${e.status}');
    messenger.showSnackBar(SnackBar(content: Text(t('mobile.anhangNichtGeladen'))));
  } on PlatformException catch (e) {
    diag('chat', 'attachment not saved: ${e.code} ${e.message}');
    messenger.showSnackBar(SnackBar(content: Text(t('mobile.nichtGespeichert'))));
  }
}

/// The pictures fetched so far, by attachment id — an attachment never
/// changes, so nothing held here is ever stale. Bounded, oldest out first: a
/// long conversation scrolled through should not keep every picture.
final _pictures = <String, Uint8List>{};
const _picturesKept = 40;

void _keep(String id, Uint8List bytes) {
  _pictures[id] = bytes;
  if (_pictures.length > _picturesKept) _pictures.remove(_pictures.keys.first);
}

/// A message's files, under its text.
class MessageFiles extends StatelessWidget {
  const MessageFiles({super.key, required this.api, required this.files});

  final CoveyApi api;
  final List<ChatAttachment> files;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(top: 8),
      child: Wrap(
        spacing: 6,
        runSpacing: 6,
        children: [
          for (final a in files)
            a.isImage ? _Picture(key: ValueKey(a.id), api: api, file: a) : FileChip(api: api, file: a),
        ],
      ),
    );
  }
}

/// A file that is not shown as a picture: its kind, name and size; a tap
/// saves it.
class FileChip extends StatelessWidget {
  const FileChip({super.key, required this.api, required this.file});

  final CoveyApi api;
  final ChatAttachment file;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final icon = file.isVideo ? AppIcons.video : AppIcons.file;
    return Material(
      color: c.surface2,
      shape: StadiumBorder(side: BorderSide(color: c.hairline)),
      clipBehavior: Clip.antiAlias,
      child: Tooltip(
        message: context.t('chat.files.download', args: {'name': file.name}),
        child: InkWell(
          onTap: () => saveChatAttachment(context, api, file),
          child: ConstrainedBox(
            constraints: const BoxConstraints(minHeight: 40, maxWidth: 280),
            child: Padding(
              padding: const EdgeInsets.fromLTRB(10, 6, 14, 6),
              child: Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  Icon(icon.of(context), size: 18, color: c.textSecondary),
                  const SizedBox(width: 6),
                  Flexible(
                    child: Text(
                      file.name,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: context.type.labelMedium?.copyWith(color: c.textPrimary),
                    ),
                  ),
                  const SizedBox(width: 6),
                  Text(fileSize(file.size), style: context.type.labelSmall?.copyWith(color: c.textSecondary)),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}

/// A picture at the message: fetched with the key, a tap opens it full
/// screen. What does not load or does not decode stands as a chip instead.
class _Picture extends StatefulWidget {
  const _Picture({super.key, required this.api, required this.file});

  final CoveyApi api;
  final ChatAttachment file;

  @override
  State<_Picture> createState() => _PictureState();
}

class _PictureState extends State<_Picture> {
  late Uint8List? _bytes = _pictures[widget.file.id];
  bool _failed = false;

  @override
  void initState() {
    super.initState();
    if (_bytes == null) _load();
  }

  Future<void> _load() async {
    try {
      final bytes = await widget.api.chatAttachment(widget.file.id);
      _keep(widget.file.id, bytes);
      if (mounted) setState(() => _bytes = bytes);
    } on ApiException catch (e) {
      diag('chat', 'picture not loaded: ${e.status}');
      if (mounted) setState(() => _failed = true);
    }
  }

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final bytes = _bytes;
    final chip = FileChip(api: widget.api, file: widget.file);
    if (_failed) return chip;
    return ClipRRect(
      borderRadius: BorderRadius.circular(12),
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 240, maxHeight: 240, minWidth: 64, minHeight: 64),
        child: bytes == null
            ? SizedBox(width: 160, height: 120, child: ColoredBox(color: c.surface1))
            : Semantics(
                image: true,
                button: true,
                label: widget.file.name,
                child: GestureDetector(
                  onTap: () => Navigator.of(context).push(
                    MaterialPageRoute<void>(
                      fullscreenDialog: true,
                      builder: (_) => AttachmentViewer(api: widget.api, file: widget.file, bytes: bytes),
                    ),
                  ),
                  child: Image.memory(
                    bytes,
                    fit: BoxFit.cover,
                    gaplessPlayback: true,
                    errorBuilder: (context, error, stack) => chip,
                  ),
                ),
              ),
      ),
    );
  }
}

/// A picture full screen: pinch to zoom, and saved from the bar.
class AttachmentViewer extends StatelessWidget {
  const AttachmentViewer({super.key, required this.api, required this.file, required this.bytes});

  final CoveyApi api;
  final ChatAttachment file;
  final Uint8List bytes;

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: Colors.black,
      appBar: AppBar(
        backgroundColor: Colors.black,
        foregroundColor: Colors.white,
        title: Text(file.name, overflow: TextOverflow.ellipsis),
        actions: [
          IconButton(
            tooltip: context.t('mobile.speichern'),
            icon: Icon(AppIcons.save.of(context)),
            onPressed: () => saveChatAttachment(context, api, file, bytes: bytes),
          ),
          const SizedBox(width: 4),
        ],
      ),
      body: SafeArea(
        child: InteractiveViewer(
          maxScale: 6,
          child: Center(child: Image.memory(bytes, fit: BoxFit.contain)),
        ),
      ),
    );
  }
}

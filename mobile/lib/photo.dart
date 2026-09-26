import 'dart:io';
import 'dart:math' as math;
import 'dart:ui' as ui;

import 'package:file_picker/file_picker.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:image_picker/image_picker.dart';

import 'api.dart';
import 'chrome.dart';
import 'diagnostics.dart';
import 'i18n.dart';
import 'icons.dart';
import 'models.dart';
import 'theme.dart';
import 'ui.dart';

/// A person's face (#377): their profile photo, or the monogram while there
/// is none or it is still on its way. The photo is fetched with the key and
/// kept in memory under its id — a new photo has a new id, so nothing stale
/// is ever shown.
class PersonPhoto extends StatefulWidget {
  const PersonPhoto({
    super.key,
    required this.api,
    required this.humanId,
    required this.photoId,
    required this.name,
    this.size = 36,
  });

  final CoveyApi api;
  final String humanId;
  final String? photoId;
  final String name;
  final double size;

  static final _cache = <String, Uint8List>{};

  /// Puts a photo just uploaded in front, so it does not travel back.
  static void remember(String photoId, Uint8List bytes) => _cache[photoId] = bytes;

  static String initials(String name) =>
      name.split(RegExp(r'\s+')).where((p) => p.isNotEmpty).take(2).map((p) => p[0].toUpperCase()).join();

  @override
  State<PersonPhoto> createState() => _PersonPhotoState();
}

class _PersonPhotoState extends State<PersonPhoto> {
  Uint8List? _bytes;

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void didUpdateWidget(PersonPhoto old) {
    super.didUpdateWidget(old);
    if (old.photoId != widget.photoId) _load();
  }

  Future<void> _load() async {
    final id = widget.photoId;
    _bytes = id == null ? null : PersonPhoto._cache[id];
    if (id == null || _bytes != null || widget.humanId.isEmpty) return;
    try {
      final bytes = await widget.api.humanPhoto(widget.humanId, id);
      PersonPhoto._cache[id] = bytes;
      if (mounted && widget.photoId == id) setState(() => _bytes = bytes);
    } on ApiException catch (e) {
      diag('photo', 'not loaded: ${e.status}');
    }
  }

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final bytes = _bytes;
    final initials = PersonPhoto.initials(widget.name);
    return Container(
      width: widget.size,
      height: widget.size,
      alignment: Alignment.center,
      clipBehavior: Clip.antiAlias,
      decoration: BoxDecoration(
        color: c.surface2,
        shape: BoxShape.circle,
        border: Border.all(color: c.hairline),
      ),
      child: bytes != null
          ? Image.memory(bytes, width: widget.size, height: widget.size, fit: BoxFit.cover, gaplessPlayback: true)
          : Text(
              initials.isEmpty ? '·' : initials,
              style: (widget.size > 48 ? context.type.titleLarge : context.type.labelMedium)?.copyWith(
                fontSize: widget.size * 0.36,
              ),
            ),
    );
  }
}

/// Changes the seat's photo: take one, choose one, or remove it. Returns the
/// seat as it now is, or null when nothing changed.
Future<Me?> changePhoto(BuildContext context, CoveyApi api, Me me) async {
  final strings = Strings.of(context);
  final t = strings.t;
  final messenger = ScaffoldMessenger.of(context);
  final nav = Navigator.of(context);
  final choice = await showModalBottomSheet<String>(
    context: context,
    builder: (context) => SafeArea(
      child: Padding(
        padding: const EdgeInsets.fromLTRB(16, 0, 16, 16),
        child: InsetGroup(
          children: [
            GroupRow(
              leading: Icon(AppIcons.camera.of(context)),
              title: t('mobile.fotoAufnehmen'),
              onTap: () => Navigator.pop(context, 'camera'),
            ),
            GroupRow(
              leading: Icon(AppIcons.photo.of(context)),
              title: t('mobile.fotoWaehlen'),
              onTap: () => Navigator.pop(context, 'pick'),
            ),
            if (me.photoId != null)
              GroupRow(
                leading: Icon(AppIcons.delete.of(context), color: context.colors.textDanger),
                title: t('mobile.fotoEntfernen'),
                onTap: () => Navigator.pop(context, 'remove'),
              ),
          ],
        ),
      ),
    ),
  );
  if (choice == null) return null;
  try {
    if (choice == 'remove') {
      await api.deletePhoto();
      return me.withPhoto(null);
    }
    final raw = choice == 'camera' ? await _takePhoto(strings) : await _choosePhoto();
    if (raw == null) return null;
    final ui.Image image;
    try {
      image = (await (await ui.instantiateImageCodec(raw)).getNextFrame()).image;
    } catch (e) {
      diag('photo', 'not decodable: $e');
      messenger.showSnackBar(SnackBar(content: Text(t('mobile.fotoFehler'))));
      return null;
    }
    final cropped = await nav.push<Uint8List>(
      MaterialPageRoute(fullscreenDialog: true, builder: (_) => PhotoCropScreen(image: image)),
    );
    image.dispose();
    if (cropped == null) return null;
    final id = await api.setPhoto(cropped);
    PersonPhoto.remember(id, cropped);
    return me.withPhoto(id);
  } on _CameraDenied {
    messenger.showSnackBar(SnackBar(content: Text(t('mobile.kameraVerweigert'))));
  } on ApiException catch (e) {
    diag('photo', 'not saved: ${e.status} ${e.message}');
    messenger.showSnackBar(SnackBar(content: Text(t('mobile.fotoFehler'))));
  } on PlatformException catch (e) {
    diag('photo', 'camera: ${e.code} ${e.message}');
    messenger.showSnackBar(
      SnackBar(content: Text(e.code == 'camera_access_denied' ? t('mobile.kameraVerweigert') : t('mobile.fotoFehler'))),
    );
  }
  return null;
}

class _CameraDenied implements Exception {}

const _camera = MethodChannel('covey/camera');

/// A shot from the camera, the front one where there is a choice. The Mac
/// has no system camera screen to borrow, so it opens a small camera sheet
/// of its own (MainFlutterWindow.swift).
Future<Uint8List?> _takePhoto(Strings strings) async {
  if (MacChrome.active) {
    try {
      return await _camera.invokeMethod<Uint8List>('capture', {
        'take': strings.t('mobile.kameraAufnehmen'),
        'cancel': strings.t('team.abbrechen'),
      });
    } on PlatformException catch (e) {
      if (e.code == 'denied') throw _CameraDenied();
      rethrow;
    }
  }
  final shot = await ImagePicker().pickImage(
    source: ImageSource.camera,
    preferredCameraDevice: CameraDevice.front,
    maxWidth: 2000,
    maxHeight: 2000,
    imageQuality: 92,
  );
  return shot?.readAsBytes();
}

/// A photo from the library, or on the Mac from a file.
Future<Uint8List?> _choosePhoto() async {
  if (Platform.isMacOS || Platform.isWindows || Platform.isLinux) {
    final files = await FilePicker.pickFiles(type: FileType.custom, allowedExtensions: const ['jpg', 'jpeg', 'png']);
    return files.isEmpty ? null : files.first.readAsBytes();
  }
  // Resizing makes the picker hand over a JPEG, also for an iPhone's HEIC.
  final shot = await ImagePicker().pickImage(
    source: ImageSource.gallery,
    maxWidth: 2000,
    maxHeight: 2000,
    imageQuality: 92,
  );
  return shot?.readAsBytes();
}

/// Crop to a circle: the photo moves and zooms under a round window, and
/// what the window shows becomes the photo — a square of [PhotoCropScreen.edge]
/// pixels, which the server keeps as it is.
class PhotoCropScreen extends StatefulWidget {
  const PhotoCropScreen({super.key, required this.image});

  /// The photo, decoded; the caller disposes it.
  final ui.Image image;

  static const edge = 512;

  @override
  State<PhotoCropScreen> createState() => _PhotoCropScreenState();
}

class _PhotoCropScreenState extends State<PhotoCropScreen> {
  bool _busy = false;
  final _view = TransformationController();
  double _side = 0;

  @override
  void dispose() {
    _view.dispose();
    super.dispose();
  }

  /// The photo laid out to cover the square window of side [side].
  Size _childSize(ui.Image img, double side) {
    final k = side / math.min(img.width, img.height);
    return Size(img.width * k, img.height * k);
  }

  Future<void> _done() async {
    final img = widget.image;
    if (_side == 0) return;
    setState(() => _busy = true);
    final child = _childSize(img, _side);
    // What the window shows, in the child's coordinates, then in pixels.
    final inverse = Matrix4.inverted(_view.value);
    final tl = MatrixUtils.transformPoint(inverse, Offset.zero);
    final br = MatrixUtils.transformPoint(inverse, Offset(_side, _side));
    final px = img.width / child.width;
    final src = Rect.fromPoints(tl * px, br * px);
    final recorder = ui.PictureRecorder();
    const edge = PhotoCropScreen.edge;
    Canvas(recorder).drawImageRect(
      img,
      src,
      Rect.fromLTWH(0, 0, edge.toDouble(), edge.toDouble()),
      Paint()..filterQuality = FilterQuality.high,
    );
    final out = await recorder.endRecording().toImage(edge, edge);
    final data = await out.toByteData(format: ui.ImageByteFormat.png);
    out.dispose();
    if (!mounted) return;
    Navigator.pop(context, data?.buffer.asUint8List());
  }

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final img = widget.image;
    return Scaffold(
      appBar: ChromeAppBar(
        title: Text(context.t('mobile.fotoZuschneiden')),
        actions: [
          TextButton(onPressed: _busy ? null : _done, child: Text(context.t('mobile.fotoUebernehmen'))),
          const SizedBox(width: 8),
        ],
      ),
      body: SafeArea(
        child: Column(
          children: [
            Expanded(
              child: Center(
                child: LayoutBuilder(
                  builder: (context, box) {
                    _side = math.min(math.min(box.maxWidth, box.maxHeight) - 48, 420);
                    final child = _childSize(img, _side);
                    return SizedBox.square(
                      dimension: _side,
                      child: Stack(
                        children: [
                          ClipRect(
                            child: InteractiveViewer(
                              transformationController: _view,
                              constrained: false,
                              minScale: 1,
                              maxScale: 5,
                              boundaryMargin: EdgeInsets.zero,
                              child: RawImage(image: img, width: child.width, height: child.height, fit: BoxFit.fill),
                            ),
                          ),
                          // The window: dimmed outside the circle.
                          IgnorePointer(
                            child: CustomPaint(size: Size.square(_side), painter: _CircleMask(c.surface0)),
                          ),
                        ],
                      ),
                    );
                  },
                ),
              ),
            ),
            Padding(
              padding: const EdgeInsets.fromLTRB(32, 0, 32, 24),
              child: Text(
                context.t('mobile.fotoZuschneidenHinweis'),
                textAlign: TextAlign.center,
                style: context.type.bodyMedium,
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _CircleMask extends CustomPainter {
  _CircleMask(this.ground);

  final Color ground;

  @override
  void paint(Canvas canvas, Size size) {
    final r = size.shortestSide / 2;
    final hole = Path()
      ..fillType = PathFillType.evenOdd
      ..addRect(Offset.zero & size)
      ..addOval(Rect.fromCircle(center: size.center(Offset.zero), radius: r));
    canvas.drawPath(hole, Paint()..color = ground.withValues(alpha: 0.72));
    canvas.drawCircle(
      size.center(Offset.zero),
      r - 0.5,
      Paint()
        ..style = PaintingStyle.stroke
        ..strokeWidth = 1
        ..color = Colors.white.withValues(alpha: 0.8),
    );
  }

  @override
  bool shouldRepaint(_CircleMask old) => old.ground != ground;
}

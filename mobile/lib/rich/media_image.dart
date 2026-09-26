import 'dart:typed_data';

import 'package:flutter/material.dart';

import '../api.dart';
import '../theme.dart';

/// A picture in a note (#344). `covey-media://<id>` is fetched through the API
/// with the key — the media store serves a picture to its owner only — and
/// kept in memory, since a medium never changes under its id. Any other
/// reference is a URL and loaded as one.
class MediaImage extends StatefulWidget {
  const MediaImage({super.key, required this.api, required this.ref, this.fit = BoxFit.contain, this.radius = 14});

  final CoveyApi api;
  final String ref;

  /// How the picture fills its place — contained in the text, covering as a
  /// note's cover (#372) — and its corners.
  final BoxFit fit;
  final double radius;

  static const scheme = 'covey-media://';

  /// What is already loaded, by id. Small on purpose: a note's pictures, not
  /// a gallery.
  static final _cache = <String, Uint8List>{};
  static const _cacheSize = 40;

  /// Puts freshly uploaded bytes in front, so the picture just inserted does
  /// not travel back from the server to be shown.
  static void remember(String ref, Uint8List bytes) {
    if (!ref.startsWith(scheme)) return;
    _cache.remove(ref);
    _cache[ref] = bytes;
    while (_cache.length > _cacheSize) {
      _cache.remove(_cache.keys.first);
    }
  }

  @override
  State<MediaImage> createState() => _MediaImageState();
}

class _MediaImageState extends State<MediaImage> {
  Uint8List? _bytes;
  bool _failed = false;

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void didUpdateWidget(MediaImage old) {
    super.didUpdateWidget(old);
    if (old.ref != widget.ref) {
      _bytes = null;
      _failed = false;
      _load();
    }
  }

  Future<void> _load() async {
    final ref = widget.ref;
    if (!ref.startsWith(MediaImage.scheme)) return;
    final hit = MediaImage._cache[ref];
    if (hit != null) {
      if (mounted) setState(() => _bytes = hit);
      return;
    }
    try {
      final b = await widget.api.noteMedia(ref.substring(MediaImage.scheme.length));
      MediaImage.remember(ref, b);
      if (mounted) setState(() => _bytes = b);
    } catch (_) {
      if (mounted) setState(() => _failed = true);
    }
  }

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    Widget body;
    if (!widget.ref.startsWith(MediaImage.scheme)) {
      body = Image.network(widget.ref, fit: widget.fit, errorBuilder: (_, _, _) => _broken(c));
    } else if (_bytes != null) {
      body = Image.memory(
        _bytes!,
        fit: widget.fit,
        width: widget.fit == BoxFit.cover ? double.infinity : null,
        height: widget.fit == BoxFit.cover ? double.infinity : null,
        gaplessPlayback: true,
        errorBuilder: (_, _, _) => _broken(c),
      );
    } else if (_failed) {
      body = _broken(c);
    } else {
      body = Container(height: 180, color: c.surface1);
    }
    return ClipRRect(borderRadius: BorderRadius.circular(widget.radius), child: body);
  }

  Widget _broken(CoveyColors c) => Container(
    height: 120,
    color: c.surface1,
    alignment: Alignment.center,
    child: Icon(Icons.broken_image_outlined, color: c.textMuted),
  );
}
